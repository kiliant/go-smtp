package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpclient"
)

// quitTimeout bounds the courtesy QUIT after an attempt.
const quitTimeout = 10 * time.Second

var (
	errRequireTLSNotAdvertised = errors.New("smtpdeliver: server does not advertise REQUIRETLS after TLS (RFC 8689 §4.2.1)")
	errMessageSource           = errors.New("smtpdeliver: message source failed")
)

// attemptInput is one SMTP transaction attempt on one address step.
type attemptInput struct {
	domain  string
	request *Request
	step    routeStep
	tls     *tlsAttempt
	// recipients are the request's recipients for this destination; indices
	// select the pending ones this attempt may send to.
	recipients []Recipient
	indices    []int
}

// recipientDecision is an attempt's verdict for one recipient index.
type recipientDecision struct {
	index       int
	disposition Disposition // DispositionTemporary means "still pending"
	reply       *smtp.RecipientResult
	status      smtp.EnhancedCode
	cause       error
}

// attemptOutcome is everything one attempt produced.
type attemptOutcome struct {
	record    AttemptResult
	decisions []recipientDecision
	// requireTLS classifies a failure for the REQUIRETLS tally.
	requireTLS requireTLSFailure
	// callErr ends the whole Deliver call: the caller's context ended or the
	// message source failed.
	callErr error
}

type requireTLSFailure int

const (
	requireTLSNone          requireTLSFailure = iota
	requireTLSTLS                             // TLS could not be established as required
	requireTLSNotAdvertised                   // TLS succeeded, REQUIRETLS not advertised
	requireTLSOther                           // the attempt failed for another reason
)

// runAttempt performs one RFC 5321 transaction for the pending recipients on
// one address (DELIVERY-DESIGN.md §7). The connection is owned from dial to
// QUIT. Recipients are decided only from authoritative replies; a possibly
// accepted message (smtpclient.ErrFinalStatusUnknown) is indeterminate and is
// never offered to another candidate.
func (d *Deliverer) runAttempt(ctx context.Context, in attemptInput) attemptOutcome {
	out := attemptOutcome{record: AttemptResult{
		MX:         in.step.host.name,
		Preference: in.step.host.pref,
		Address:    dialRequest(in.domain, in.step).Address,
		Stage:      StageConnect,
	}}
	defer func() {
		out.record.Policies = in.tls.report.policies()
		d.emit(Event{Kind: EventAttempt, Domain: in.domain, MX: out.record.MX, Address: out.record.Address, Cause: out.record.Cause})
	}()
	fail := func(stage AttemptStage, cause error, kind requireTLSFailure) attemptOutcome {
		out.record.Stage = stage
		out.record.Cause = cause
		out.requireTLS = kind
		if ctxErr := ctx.Err(); ctxErr != nil {
			out.callErr = ctxErr
		}
		for _, i := range in.indices {
			out.decisions = append(out.decisions, recipientDecision{index: i, disposition: DispositionTemporary, cause: cause})
		}
		return out
	}

	conn, err := d.connect(ctx, dialRequest(in.domain, in.step))
	if err != nil {
		return fail(StageConnect, err, requireTLSOther)
	}
	out.record.Stage = StageGreeting
	client, err := smtpclient.NewClient(ctx, conn, &smtpclient.ClientOptions{
		Identity:      d.identity,
		TLSServerName: in.tls.serverName,
		TLSConfig:     in.tls.config,
	})
	if err != nil {
		stage := StageGreeting
		var smtpErr *smtp.Error
		if errors.As(err, &smtpErr) && smtpErr.Command == "EHLO" {
			stage = StageHello
		}
		return fail(stage, err, requireTLSOther)
	}
	defer client.Close()
	defer func() {
		// QUIT is best effort and must not hold the call: a short bound of
		// its own, detached from a context that may already have ended.
		quitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quitTimeout)
		defer cancel()
		_ = client.Quit(quitCtx, nil)
	}()
	out.record.Stage = StageHello

	if _, ok := client.Extension(smtp.ExtStartTLS); ok {
		out.record.Stage = StageTLS
		err := client.StartTLS(ctx, &smtpclient.StartTLSOptions{TLSConfig: in.tls.config, ServerName: in.tls.serverName})
		d.emit(Event{Kind: EventTLS, Domain: in.domain, MX: in.step.host.name, Address: out.record.Address, Cause: err})
		if err != nil {
			// A failed advertised handshake is never retried in cleartext on
			// the same address (DELIVERY-DESIGN.md §6).
			return fail(StageTLS, err, requireTLSTLS)
		}
	} else if err := in.tls.noSTARTTLS(); err != nil {
		out.record.Stage = StageTLS
		return fail(StageTLS, err, requireTLSTLS)
	}

	requireTLS := wantsRequireTLS(in.request.MailOptions)
	if requireTLS {
		if _, ok := client.Extension(smtp.ExtRequireTLS); !ok {
			return fail(StageHello, errRequireTLSNotAdvertised, requireTLSNotAdvertised)
		}
	}

	out.record.Stage = StageMail
	if err := client.Mail(ctx, in.request.EnvelopeFrom, mailOptionsFor(in.request, client), nil); err != nil {
		var smtpErr *smtp.Error
		if errors.As(err, &smtpErr) && smtpErr.IsPermanent() {
			// A permanent MAIL reply is final for every pending recipient.
			for _, i := range in.indices {
				reply := replyFromError(smtpErr, "MAIL")
				reply.Recipient = in.recipients[i].Address
				out.decisions = append(out.decisions, recipientDecision{index: i, disposition: DispositionPermanent, reply: reply})
			}
			out.record.Stage = StageMail
			return out
		}
		return fail(StageMail, err, requireTLSOther)
	}

	out.record.Stage = StageRcpt
	batch := make([]smtpclient.Recipient, len(in.indices))
	for k, i := range in.indices {
		batch[k] = smtpclient.Recipient{Address: in.recipients[i].Address, Options: in.recipients[i].Options}
	}
	replies, err := client.RcptBatch(ctx, batch, nil)
	if err != nil {
		return fail(StageRcpt, err, requireTLSOther)
	}
	var accepted []int // positions in in.indices, in RCPT order
	for k, r := range replies {
		reply := r
		switch {
		case r.Code >= 200 && r.Code < 300:
			accepted = append(accepted, k)
		case r.Code >= 500:
			out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionPermanent, reply: &reply})
		default:
			out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionTemporary, reply: &reply})
		}
	}
	if len(accepted) == 0 {
		return out // the transaction ended at RCPT; no content was sent
	}

	out.record.Stage = StageContent
	src, err := in.request.Message.Open(ctx, &OpenMessageOptions{})
	if err != nil || src == nil {
		if err == nil {
			err = errors.New("smtpdeliver: MessageSource.Open returned a nil reader")
		}
		return d.notAttempted(ctx, out, in, accepted, fmt.Errorf("%w: %w", errMessageSource, err))
	}
	reader := &sourceReader{r: src}
	result, err := client.Data(ctx, reader, nil)
	_ = src.Close()
	if srcErr := reader.failure(); srcErr != nil {
		// The content never completed, so no recipient received it.
		return d.notAttempted(ctx, out, in, accepted, fmt.Errorf("%w: %w", errMessageSource, srcErr))
	}
	out.record.Stage = StageComplete
	return d.applyContentResult(ctx, out, in, accepted, result, err)
}

// applyContentResult maps a Data result onto the accepted recipients.
func (d *Deliverer) applyContentResult(ctx context.Context, out attemptOutcome, in attemptInput, accepted []int, result smtp.DataResult, err error) attemptOutcome {
	unknown := errors.Is(err, smtpclient.ErrFinalStatusUnknown)
	for pos, k := range accepted {
		idx := in.indices[k]
		switch {
		case pos < len(result):
			// An authoritative per-recipient reply: complete on success, or
			// the received prefix when the final status of the rest is
			// unknown (RFC 2033 §4.2).
			reply := result[pos]
			out.decisions = append(out.decisions, contentDecision(idx, &reply))
		case unknown:
			// The server may have accepted the message (RFC 5321 §4.2.5).
			out.decisions = append(out.decisions, recipientDecision{index: idx, disposition: DispositionIndeterminate, cause: err})
		default:
			var smtpErr *smtp.Error
			if errors.As(err, &smtpErr) && smtpErr.Code != 0 {
				reply := replyFromError(smtpErr, smtpErr.Command)
				reply.Recipient = in.recipients[idx].Address
				out.decisions = append(out.decisions, contentDecision(idx, reply))
				continue
			}
			// A failure before completion could start: nothing was accepted.
			out.decisions = append(out.decisions, recipientDecision{index: idx, disposition: DispositionTemporary, cause: err})
		}
	}
	if err != nil {
		out.record.Cause = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			out.callErr = ctxErr
		}
	}
	return out
}

func contentDecision(idx int, reply *smtp.RecipientResult) recipientDecision {
	switch {
	case reply.Code >= 200 && reply.Code < 300:
		return recipientDecision{index: idx, disposition: DispositionDelivered, reply: reply}
	case reply.Code >= 500:
		return recipientDecision{index: idx, disposition: DispositionPermanent, reply: reply}
	default:
		return recipientDecision{index: idx, disposition: DispositionTemporary, reply: reply}
	}
}

// notAttempted ends the call after a message-source failure: recipients
// accepted at RCPT received nothing; every other pending recipient was not
// reached either.
func (d *Deliverer) notAttempted(ctx context.Context, out attemptOutcome, in attemptInput, accepted []int, cause error) attemptOutcome {
	out.record.Stage = StageContent
	out.record.Cause = cause
	out.callErr = cause
	if ctxErr := ctx.Err(); ctxErr != nil {
		out.callErr = ctxErr
	}
	for _, k := range accepted {
		out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionNotAttempted, cause: cause})
	}
	return out
}

// sourceReader remembers the message source's own read failure, so it can be
// told apart from a network failure while sending the same bytes.
type sourceReader struct {
	r   io.Reader
	mu  sync.Mutex
	err error
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}
	return n, err
}

func (s *sourceReader) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// mailOptionsFor clones the request's MAIL options for one session and sets
// SIZE (RFC 1870) only when the server advertised it: a declaration the
// server did not ask for is not sent.
func mailOptionsFor(req *Request, client *smtpclient.Client) *smtp.MailOptions {
	var opts smtp.MailOptions
	if req.MailOptions != nil {
		opts = *req.MailOptions
	}
	var transport smtp.TransportOptions
	if opts.Transport != nil {
		transport = *opts.Transport
	}
	if transport.Size == nil {
		transport.Size = req.Message.Size
	}
	if _, ok := client.Extension(smtp.ExtSize); !ok {
		transport.Size = nil
	}
	opts.Transport = &transport
	return &opts
}

func wantsRequireTLS(opts *smtp.MailOptions) bool {
	return opts != nil && opts.Delivery != nil && opts.Delivery.RequireTLS
}

// replyFromError records a received reply carried by an *smtp.Error.
func replyFromError(e *smtp.Error, command string) *smtp.RecipientResult {
	return &smtp.RecipientResult{Command: command, Code: e.Code, Enhanced: e.Enhanced, Text: e.Text}
}
