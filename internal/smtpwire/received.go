package smtpwire

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrReceivedField = errors.New("smtpwire: invalid Received field")
	ErrReceivedState = errors.New("smtpwire: impossible Received session state")
)

// ReceivedOptions contains the session facts used to generate one RFC 5321
// trace field. The transmission type is derived from these facts and cannot be
// supplied as an arbitrary string.
type ReceivedOptions struct {
	From           string
	By             string
	Extended       bool
	LMTP           bool
	TLS            bool
	Authenticated  bool
	ID             string
	For            string
	RecipientCount int
	Timestamp      time.Time
}

// FormatReceived returns one complete Received header including its trailing
// CRLF. The FOR clause is emitted only for exactly one recipient.
func FormatReceived(opts ReceivedOptions) (string, error) {
	if opts.From == "" || opts.By == "" || opts.Timestamp.IsZero() || opts.RecipientCount < 0 {
		return "", ErrReceivedField
	}
	for _, field := range []string{opts.From, opts.By, opts.ID, opts.For} {
		if strings.ContainsAny(field, "\r\n\x00") {
			return "", ErrReceivedField
		}
	}
	with, err := receivedProtocol(opts)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("Received: from ")
	b.WriteString(opts.From)
	b.WriteString(" by ")
	b.WriteString(opts.By)
	b.WriteString(" with ")
	b.WriteString(with)
	if opts.ID != "" {
		b.WriteString(" id ")
		b.WriteString(opts.ID)
	}
	if opts.RecipientCount == 1 && opts.For != "" {
		if opts.For[0] != '<' || opts.For[len(opts.For)-1] != '>' {
			return "", fmt.Errorf("%w: FOR must be an angle path", ErrReceivedField)
		}
		b.WriteString(" for ")
		b.WriteString(opts.For)
	}
	b.WriteString("; ")
	b.WriteString(opts.Timestamp.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	b.WriteString("\r\n")
	return b.String(), nil
}

func receivedProtocol(opts ReceivedOptions) (string, error) {
	if opts.LMTP {
		switch {
		case opts.TLS && opts.Authenticated:
			return "LMTPSA", nil
		case opts.TLS:
			return "LMTPS", nil
		case opts.Authenticated:
			return "LMTPA", nil
		default:
			return "LMTP", nil
		}
	}
	if !opts.Extended {
		if opts.TLS || opts.Authenticated {
			return "", ErrReceivedState
		}
		return "SMTP", nil
	}
	switch {
	case opts.TLS && opts.Authenticated:
		return "ESMTPSA", nil
	case opts.TLS:
		return "ESMTPS", nil
	case opts.Authenticated:
		return "ESMTPA", nil
	default:
		return "ESMTP", nil
	}
}
