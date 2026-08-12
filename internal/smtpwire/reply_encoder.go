package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var (
	ErrReplyEncodeCode       = errors.New("smtpwire: invalid reply code for encoding")
	ErrReplyEncodeText       = errors.New("smtpwire: reply text contains CR, LF, or NUL")
	ErrEnhancedCodeSyntax    = errors.New("smtpwire: invalid enhanced status code")
	ErrEnhancedClassMismatch = errors.New("smtpwire: enhanced status class disagrees with reply code")
)

// ReplyContext identifies the RFC 2034 contexts in which an enhanced status
// prefix must be omitted. The zero value is an ordinary command reply.
type ReplyContext uint8

const (
	ReplyContextCommand ReplyContext = iota
	ReplyContextGreeting
	ReplyContextHello
)

// ReplyOptions controls semantic reply formatting. Enhanced, when non-nil,
// is prefixed to every reply line except greetings and HELO/EHLO replies, where
// RFC 2034 requires omission. The primary and enhanced classes must agree.
type ReplyOptions struct {
	Enhanced *EnhancedCode
	Context  ReplyContext
}

// EncodeReply writes RFC 5321 multiline reply framing. It validates the whole
// reply before writing anything, so invalid text cannot partially desynchronise
// a session.
func EncodeReply(w io.Writer, reply Reply, opts ReplyOptions) error {
	if !validReplyCode(reply.Code) {
		return fmt.Errorf("%w: %d", ErrReplyEncodeCode, reply.Code)
	}
	lines := reply.Lines
	if len(lines) == 0 {
		lines = []string{reply.Text}
	}
	lines = append([]string(nil), lines...)
	for i, line := range lines {
		if strings.ContainsAny(line, "\r\n\x00") {
			return fmt.Errorf("%w: line %d", ErrReplyEncodeText, i+1)
		}
	}

	if opts.Enhanced != nil && opts.Context == ReplyContextCommand {
		prefix, err := formatEnhancedCode(reply.Code, *opts.Enhanced)
		if err != nil {
			return err
		}
		for i := range lines {
			if lines[i] == "" {
				lines[i] = prefix
			} else {
				lines[i] = prefix + " " + lines[i]
			}
		}
	}

	var b strings.Builder
	for i, line := range lines {
		b.WriteString(strconv.Itoa(reply.Code))
		if i+1 < len(lines) {
			b.WriteByte('-')
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func validReplyCode(code int) bool {
	return code >= 200 && code <= 559 && (code/10)%10 <= 5
}

func formatEnhancedCode(replyCode int, code EnhancedCode) (string, error) {
	if (code.Class != 2 && code.Class != 4 && code.Class != 5) || code.Subject < 0 || code.Subject > 999 || code.Detail < 0 || code.Detail > 999 {
		return "", fmt.Errorf("%w: %d.%d.%d", ErrEnhancedCodeSyntax, code.Class, code.Subject, code.Detail)
	}
	if replyCode/100 != code.Class {
		return "", fmt.Errorf("%w: reply %d with %d.%d.%d", ErrEnhancedClassMismatch, replyCode, code.Class, code.Subject, code.Detail)
	}
	return fmt.Sprintf("%d.%d.%d", code.Class, code.Subject, code.Detail), nil
}
