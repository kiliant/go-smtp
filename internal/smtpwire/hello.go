package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrHelloVerb     = errors.New("smtpwire: command is not HELO, EHLO, or LHLO")
	ErrHelloArgument = errors.New("smtpwire: invalid HELO-family argument")
	ErrEHLOEncode    = errors.New("smtpwire: invalid EHLO advertisement")
)

// Hello is a parsed HELO, EHLO, or LHLO command. Verb is normalised to upper
// case; Domain preserves the peer's spelling.
type Hello struct {
	Verb   string
	Domain string
}

// ParseHelloCommand parses the EHLO command family. Domain is kept opaque at
// this layer: domain-name and address-literal policy belongs to the session.
func ParseHelloCommand(command Command) (Hello, error) {
	verb := strings.ToUpper(command.Verb)
	switch verb {
	case "HELO", "EHLO", "LHLO":
	default:
		return Hello{}, fmt.Errorf("%w: %q", ErrHelloVerb, command.Verb)
	}
	if command.Argument == "" || strings.Contains(command.Argument, " ") {
		return Hello{}, fmt.Errorf("%w: %q", ErrHelloArgument, command.Argument)
	}
	return Hello{Verb: verb, Domain: command.Argument}, nil
}

// EncodeEHLOReply serialises a 250 EHLO advertisement. The first line is the
// greeting domain and optional greeting text; every following line is one
// open-ended extension. Raw takes precedence over Params when present.
func EncodeEHLOReply(w io.Writer, reply EHLOReply) error {
	if reply.Domain == "" || strings.ContainsAny(reply.Domain, " \r\n\x00") || strings.ContainsAny(reply.Greeting, "\r\n\x00") {
		return ErrEHLOEncode
	}
	lines := make([]string, 0, 1+len(reply.Extensions))
	first := reply.Domain
	if reply.Greeting != "" {
		first += " " + reply.Greeting
	}
	lines = append(lines, first)
	for _, ext := range reply.Extensions {
		if err := validateKeyword(ext.Keyword); err != nil {
			return fmt.Errorf("%w: %v", ErrEHLOEncode, err)
		}
		line := ext.Keyword
		if ext.Raw != "" {
			if strings.ContainsAny(ext.Raw, "\r\n\x00") {
				return ErrEHLOEncode
			}
			line += " " + ext.Raw
		} else if len(ext.Params) > 0 {
			for _, param := range ext.Params {
				if param == "" || strings.ContainsAny(param, " \r\n\x00") {
					return ErrEHLOEncode
				}
			}
			line += " " + strings.Join(ext.Params, " ")
		}
		lines = append(lines, line)
	}
	return EncodeReply(w, Reply{Code: 250, Lines: lines}, ReplyOptions{Context: ReplyContextHello})
}
