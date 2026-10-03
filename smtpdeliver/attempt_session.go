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
	errUnexpectedRcptReply     = errors.New("smtpdeliver: RCPT accepted with a reply other than 250 or 251; the transaction is abandoned before content")
	errResultMismatch          = errors.New("smtpdeliver: final replies cannot be matched to accepted recipients")
	errNoSMTPUTF8              = errors.New("smtpdeliver: server does not support SMTPUTF8, which the message requires (RFC 6531 §3.5)")
	errNo8BitMIME              = errors.New("smtpdeliver: server does not support the message's 8BITMIME or BINARYMIME body (RFC 6152 §3, RFC 3030)")
)

// Enhanced status codes for capability exhaustion: RFC 6531 §3.5 and RFC
// 3463 §3.7 (X.6.3, conversion required but not supported).
var (
	statusNoSMTPUTF8 = smtp.ParseEnhancedCode("5.6.7")
	statusNo8BitMIME = smtp.ParseEnhancedCode("5.6.3")
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

// failureKind classifies why an attempt failed, for the destination's
// exhaustion rules (DELIVERY-DESIGN.md, implementation decisions).
type failureKind int

const (
	failureNone failureKind = iota
	// failureOther: a refused connection, a 4yz, a network error; a later
	// attempt may succeed.
	failureOther
	// failureTLSRequirement: TLS could not be established as REQUIRETLS (RFC
	// 8689 §4.2.1) needs: STARTTLS not offered, a 5yz to STARTTLS, or a
	// certificate that failed authentication.
	failureTLSRequirement
	// failureRequireTLSNotAdvertised: TLS succeeded but REQUIRETLS was not
	// advertised.
	failureRequireTLSNotAdvertised
	// failureCapability: the server lacks an extension the message itself
	// needs (SMTPUTF8, 8BITMIME, BINARYMIME).
	failureCapability
)

// attemptOutcome is everything one attempt produced.
type attemptOutcome struct {
	record    AttemptResult
	decisions []recipientDecision
	kind      failureKind
	// capabilityStatus is the status for a failureCapability.
	capabilityStatus smtp.EnhancedCode
	// metRequireTLS: this candidate satisfied every REQUIRETLS requirement,
	// so the destination can never be "no MX meets the requirements". Every
	// later failure on such a candidate also counts as failureOther, which
	// has the same effect; this flag states the rule directly rather than
	// relying on that.
	metRequireTLS bool
	// callErr ends the whole Deliver call: the caller's context ended or the
	// message source failed.
	callErr error
}

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

	// fail ends the attempt with every pending recipient still pending. A
	// received reply is kept as each recipient's Reply and is not the
	// attempt's Cause: the attempt ended on a server reply.
	fail := func(stage AttemptStage, err error, kind failureKind) attemptOutcome {
		out.record.Stage = stage
		out.kind = kind
		var smtpErr *smtp.Error
		coded := errors.As(err, &smtpErr) && smtpErr.Code != 0 && !errors.Is(err, smtpclient.ErrFinalStatusUnknown)
		if !coded {
			out.record.Cause = err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			out.callErr = ctxErr
		}
		for _, i := range in.indices {
			dec := recipientDecision{index: i, disposition: DispositionTemporary, cause: err}
			if coded {
				dec.reply = replyFromError(smtpErr, smtpErr.Command)
				dec.reply.Recipient = in.recipients[i].Address
				dec.cause = nil
			}
			out.decisions = append(out.decisions, dec)
		}
		return out
	}

	conn, err := d.connect(ctx, dialRequest(in.domain, in.step))
	if err != nil {
		return fail(StageConnect, err, failureOther)
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
		return fail(stage, err, failureOther)
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

	if hasExtension(client, smtp.ExtStartTLS) {
		out.record.Stage = StageTLS
		err := client.StartTLS(ctx, &smtpclient.StartTLSOptions{TLSConfig: in.tls.config, ServerName: in.tls.serverName})
		d.emit(Event{Kind: EventTLS, Domain: in.domain, MX: in.step.host.name, Address: out.record.Address, Cause: err})
		if err != nil {
			// A failed advertised STARTTLS is never followed by cleartext on
			// the same address (DELIVERY-DESIGN.md §6).
			return fail(StageTLS, err, classifyTLSFailure(err))
		}
	} else if err := in.tls.noSTARTTLS(); err != nil {
		return fail(StageTLS, err, failureTLSRequirement)
	}

	if wantsRequireTLS(in.request.MailOptions) {
		if !hasExtension(client, smtp.ExtRequireTLS) {
			return fail(StageHello, errRequireTLSNotAdvertised, failureRequireTLSNotAdvertised)
		}
		out.metRequireTLS = true
	}
	if status, err := missingCapability(in, client); err != nil {
		out.capabilityStatus = status
		return fail(StageHello, err, failureCapability)
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
			return out
		}
		return fail(StageMail, err, failureOther)
	}

	out.record.Stage = StageRcpt
	dsn := hasExtension(client, smtp.ExtDSN)
	batch := make([]smtpclient.Recipient, len(in.indices))
	for k, i := range in.indices {
		batch[k] = smtpclient.Recipient{Address: in.recipients[i].Address, Options: rcptOptionsFor(in.recipients[i].Options, dsn)}
	}
	replies, err := client.RcptBatch(ctx, batch, nil)
	if err != nil {
		return fail(StageRcpt, err, failureOther)
	}
	var accepted []int // positions in in.indices, in RCPT order
	odd := false
	for k, r := range replies {
		switch {
		case r.Code == 250 || r.Code == 251:
			accepted = append(accepted, k)
		case r.Code >= 200 && r.Code < 300:
			odd = true
		}
	}
	for k, r := range replies {
		reply := r
		switch {
		case r.Code >= 500:
			out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionPermanent, reply: &reply})
		case r.Code == 250 || r.Code == 251:
			if odd {
				out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionTemporary, cause: errUnexpectedRcptReply})
			}
		default:
			out.decisions = append(out.decisions, recipientDecision{index: in.indices[k], disposition: DispositionTemporary, reply: &reply})
		}
	}
	if odd {
		// smtpclient keeps only 250 and 251 in the transaction, so another
		// 2yz leaves the two sides disagreeing about who is a recipient.
		// Sending content now could deliver to someone this side cannot
		// account for; abandon before DATA and let QUIT discard it.
		out.kind = failureOther
		return out
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
	return d.applyContentResult(ctx, out, in, accepted, result, err)
}

// classifyTLSFailure sorts a STARTTLS failure for the REQUIRETLS rule: an
// authentication failure or a 5yz is a requirement failure; a 4yz or a
// network error says nothing about the server's ability to meet them.
func classifyTLSFailure(err error) failureKind {
	var smtpErr *smtp.Error
	if errors.As(err, &smtpErr) && smtpErr.Code != 0 {
		if smtpErr.IsPermanent() {
			return failureTLSRequirement
		}
		return failureOther
	}
	for _, auth := range []error{errDANEVerify, errMTASTSCertificate, errRequireTLSAuth} {
		if errors.Is(err, auth) {
			return failureTLSRequirement
		}
	}
	return failureOther
}

// missingCapability reports an extension the message needs that the server
// lacks. Sending anyway would violate RFC 6531 §3.5 or RFC 6152 §3.
func missingCapability(in attemptInput, client *smtpclient.Client) (smtp.EnhancedCode, error) {
	var transport smtp.TransportOptions
	if in.request.MailOptions != nil && in.request.MailOptions.Transport != nil {
		transport = *in.request.MailOptions.Transport
	}
	if transport.SMTPUTF8 && !hasExtension(client, smtp.ExtSMTPUTF8) {
		return statusNoSMTPUTF8, errNoSMTPUTF8
	}
	switch transport.Body {
	case smtp.BodyType8BitMIME:
		if !hasExtension(client, smtp.Ext8BitMIME) {
			return statusNo8BitMIME, errNo8BitMIME
		}
	case smtp.BodyTypeBinaryMIME:
		if !hasExtension(client, smtp.ExtBinaryMIME) || !hasExtension(client, smtp.ExtChunking) {
			return statusNo8BitMIME, errNo8BitMIME
		}
	}
	return smtp.EnhancedCode{}, nil
}

func hasExtension(client *smtpclient.Client, ext smtp.Extension) bool {
	_, ok := client.Extension(ext)
	return ok
}

// applyContentResult maps a Data result onto the accepted recipients. A
// result that cannot be matched to them one to one is treated as unknown:
// the server may have accepted the message for anyone involved.
func (d *Deliverer) applyContentResult(ctx context.Context, out attemptOutcome, in attemptInput, accepted []int, result smtp.DataResult, err error) attemptOutcome {
	unknown := errors.Is(err, smtpclient.ErrFinalStatusUnknown)
	var smtpErr *smtp.Error
	coded := !unknown && errors.As(err, &smtpErr) && smtpErr.Code != 0
	if err == nil && !resultMatches(result, accepted, in) {
		unknown, err = true, errResultMismatch
		result = nil
	}

	for pos, k := range accepted {
		idx := in.indices[k]
		authoritative := (err == nil || unknown) && pos < len(result) && result[pos].Recipient == in.recipients[idx].Address
		switch {
		case authoritative:
			// An authoritative per-recipient reply: complete on success, or
			// the received prefix when the rest is unknown (RFC 2033 §4.2).
			reply := result[pos]
			out.decisions = append(out.decisions, contentDecision(idx, &reply))
		case unknown:
			// The server may have accepted the message (RFC 5321 §4.2.5).
			out.decisions = append(out.decisions, recipientDecision{index: idx, disposition: DispositionIndeterminate, cause: err})
		case coded:
			reply := replyFromError(smtpErr, smtpErr.Command)
			reply.Recipient = in.recipients[idx].Address
			out.decisions = append(out.decisions, contentDecision(idx, reply))
		default:
			// A failure before completion could start: nothing was accepted.
			out.decisions = append(out.decisions, recipientDecision{index: idx, disposition: DispositionTemporary, cause: err})
		}
	}

	// The attempt ended on server replies when final replies arrived;
	// otherwise it ended on a failure, which is its Cause.
	if err == nil || coded {
		out.record.Stage = StageComplete
	} else {
		out.record.Cause = err
		if !unknown {
			out.kind = failureOther
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil && err != nil {
		out.callErr = ctxErr
	}
	return out
}

// resultMatches reports whether a complete Data result lines up one to one
// with the accepted recipients.
func resultMatches(result smtp.DataResult, accepted []int, in attemptInput) bool {
	if len(result) != len(accepted) {
		return false
	}
	for pos, k := range accepted {
		if result[pos].Recipient != in.recipients[in.indices[k]].Address {
			return false
		}
	}
	return true
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

// mailOptionsFor clones the request's MAIL options for one session. SIZE
// (RFC 1870) is declared only when the server advertised it, and the DSN
// parameters (RFC 3461) are left out when it does not support DSN: the
// message is then relayed without them, as RFC 3461 §4 intends for a non-DSN
// next hop.
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
	if !hasExtension(client, smtp.ExtSize) {
		transport.Size = nil
	}
	opts.Transport = &transport
	if opts.Delivery != nil && opts.Delivery.DSN != nil && !hasExtension(client, smtp.ExtDSN) {
		delivery := *opts.Delivery
		delivery.DSN = nil
		opts.Delivery = &delivery
	}
	return &opts
}

// rcptOptionsFor clones one recipient's options, leaving out the DSN
// parameters for a server without DSN (RFC 3461 §4).
func rcptOptionsFor(opts *smtp.RcptOptions, dsn bool) *smtp.RcptOptions {
	if opts == nil || dsn || opts.Delivery == nil || opts.Delivery.DSN == nil {
		return opts
	}
	clone := *opts
	delivery := *opts.Delivery
	delivery.DSN = nil
	clone.Delivery = &delivery
	return &clone
}

func wantsRequireTLS(opts *smtp.MailOptions) bool {
	return opts != nil && opts.Delivery != nil && opts.Delivery.RequireTLS
}

// replyFromError records a received reply carried by an *smtp.Error.
func replyFromError(e *smtp.Error, command string) *smtp.RecipientResult {
	return &smtp.RecipientResult{Command: command, Code: e.Code, Enhanced: e.Enhanced, Text: e.Text}
}
