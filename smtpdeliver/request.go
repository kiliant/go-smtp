package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

// Request is one message to deliver to one or more destination domains
// (RFC 5321 §3.3). Every recipient of every destination receives the same
// message content and envelope sender.
//
// Callers constructing a Request literal must use keyed fields.
type Request struct {
	// EnvelopeFrom is the RFC 5321 §4.1.1.2 reverse-path. Empty sends the null
	// reverse-path "<>".
	EnvelopeFrom string
	// MailOptions carries MAIL FROM parameters (RFC 5321 §4.1.1.2). It is
	// cloned per attempt. Its Transport.Size, if set, must agree with
	// Message.Size.
	MailOptions *smtp.MailOptions
	// Message supplies the content, opened afresh for every transfer attempt.
	Message MessageSource
	// Destinations are the routing groups, at most one per domain, compared
	// case-insensitively (RFC 5321 §2.4).
	Destinations []Destination

	_ struct{}
}

// Destination is one routing group: the recipients reached through one RFC
// 5321 §5 routing domain.
//
// Callers constructing a Destination literal must use keyed fields.
type Destination struct {
	// Domain is the absolute ASCII routing domain resolved for MX records and
	// used as the RFC 8461 Policy Domain. A trailing dot is optional; search
	// suffixes are never applied. A caller accepting internationalised domains
	// supplies the A-label (RFC 5890). Address literals are not accepted.
	//
	// It is supplied explicitly rather than derived from recipient addresses,
	// which may quote "@" in local-parts, use SMTPUTF8 spellings, or be routed
	// through a smart host whose Policy Domain differs.
	Domain string
	// Recipients are the forward-paths (RFC 5321 §4.1.1.3) for this domain.
	// Duplicates are kept and each receives its own outcome.
	Recipients []Recipient

	_ struct{}
}

// Recipient is one RFC 5321 §4.1.1.3 forward-path and its RCPT parameters.
//
// Callers constructing a Recipient literal must use keyed fields.
type Recipient struct {
	// Address is the forward-path mailbox, sent in RCPT TO.
	Address string
	// Options carries RCPT TO parameters (RFC 5321 §4.1.1.3). It is cloned
	// per attempt.
	Options *smtp.RcptOptions

	_ struct{}
}

// MessageSource supplies message content (RFC 5322) for every transfer
// attempt. It replaces a one-shot io.Reader because failover to another MX
// after a temporary final reply resends the message, and this package does
// not buffer it.
//
// Callers constructing a MessageSource literal must use keyed fields.
type MessageSource struct {
	// Open returns a fresh reader positioned at the start of the content. It
	// is called once per transfer attempt and must yield the same octets each
	// time. The Deliverer closes every reader it opens.
	Open func(ctx context.Context, opts *OpenMessageOptions) (io.ReadCloser, error)
	// Size, when non-nil, is the content size in octets, declared with the RFC
	// 1870 SIZE parameter. It must be non-negative.
	Size *int64

	_ struct{}
}

// OpenMessageOptions is passed to MessageSource.Open. It has no fields yet;
// the struct exists so a future attempt detail is an added field rather than
// a changed callback signature (RFC 5321 §3.3 content transfer). A nil
// *OpenMessageOptions means defaults.
type OpenMessageOptions struct {
	_ struct{}
}

// DeliverOptions configures one Deliverer.Deliver call (RFC 5321 §3.3). It
// has no fields yet. A nil *DeliverOptions means defaults.
type DeliverOptions struct {
	_ struct{}
}

// RefreshPolicyOptions configures one Deliverer.RefreshPolicy call (RFC 8461
// §5.1). It has no fields yet. A nil *RefreshPolicyOptions means defaults.
type RefreshPolicyOptions struct {
	_ struct{}
}

// destination is a validated Destination with its normalised domain.
type destination struct {
	domain     string
	recipients []Recipient
}

// validateRequest checks request before any DNS or wire I/O and returns the
// normalised destinations.
func (d *Deliverer) validateRequest(request *Request) ([]destination, error) {
	if request == nil {
		return nil, errors.New("smtpdeliver: nil Request")
	}
	if request.Message.Open == nil {
		return nil, errors.New("smtpdeliver: Request.Message.Open is nil")
	}
	if size := request.Message.Size; size != nil {
		if *size < 0 {
			return nil, fmt.Errorf("smtpdeliver: negative Request.Message.Size %d", *size)
		}
		if mo := request.MailOptions; mo != nil && mo.Transport != nil && mo.Transport.Size != nil && *mo.Transport.Size != *size {
			return nil, fmt.Errorf("smtpdeliver: Request.Message.Size %d disagrees with MailOptions.Transport.Size %d", *size, *mo.Transport.Size)
		}
	}
	if len(request.Destinations) == 0 {
		return nil, errors.New("smtpdeliver: Request has no destinations")
	}
	if len(request.Destinations) > d.maxDestinations {
		return nil, fmt.Errorf("smtpdeliver: Request has %d destinations, more than the limit of %d", len(request.Destinations), d.maxDestinations)
	}
	out := make([]destination, 0, len(request.Destinations))
	seen := make(map[string]int, len(request.Destinations))
	for i, dest := range request.Destinations {
		domain, err := normalizeDomain(dest.Domain)
		if err != nil {
			return nil, fmt.Errorf("smtpdeliver: Destinations[%d]: %w", i, err)
		}
		if j, dup := seen[domain]; dup {
			return nil, fmt.Errorf("smtpdeliver: Destinations[%d] repeats domain %q of Destinations[%d]", i, domain, j)
		}
		seen[domain] = i
		if len(dest.Recipients) == 0 {
			return nil, fmt.Errorf("smtpdeliver: Destinations[%d] (%s) has no recipients", i, domain)
		}
		for k, rcpt := range dest.Recipients {
			if rcpt.Address == "" {
				return nil, fmt.Errorf("smtpdeliver: Destinations[%d].Recipients[%d] has an empty address", i, k)
			}
		}
		out = append(out, destination{domain: domain, recipients: append([]Recipient(nil), dest.Recipients...)})
	}
	return out, nil
}

// normalizeDomain validates an absolute ASCII host name (RFC 1123 §2.1) and
// returns it in lower case without a trailing dot.
func normalizeDomain(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty domain")
	}
	if strings.HasPrefix(name, "[") {
		return "", fmt.Errorf("domain %q is an address literal, which is not supported", name)
	}
	trimmed := strings.TrimSuffix(name, ".")
	if trimmed == "" || len(trimmed) > 253 {
		return "", fmt.Errorf("domain %q has an invalid length", name)
	}
	if _, err := netip.ParseAddr(trimmed); err == nil {
		return "", fmt.Errorf("domain %q is an IP address, not a domain", name)
	}
	for _, label := range strings.Split(trimmed, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("domain %q has an empty or over-long label", name)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("domain %q has a label starting or ending with '-'", name)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return "", fmt.Errorf("domain %q contains %q; only ASCII letters, digits and '-' are allowed (supply the A-label for internationalised names)", name, c)
			}
		}
	}
	return strings.ToLower(trimmed), nil
}
