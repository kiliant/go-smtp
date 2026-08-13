package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	// ErrCommandLineTooLong means an unauthenticated command exceeded the
	// configured line bound before its terminating CRLF.
	ErrCommandLineTooLong = errors.New("smtpwire: command line too long")
	// ErrCommandBareLF means a command used LF without the required preceding
	// CR. Accepting two notions of a command boundary is an SMTP-smuggling
	// primitive, so server-direction framing is strict here.
	ErrCommandBareLF = errors.New("smtpwire: command line ended with bare LF")
	// ErrCommandEmpty means the peer sent an empty CRLF line.
	ErrCommandEmpty = errors.New("smtpwire: empty command line")
	// ErrCommandVerbSyntax means the command verb was not a non-empty ASCII
	// alphanumeric/hyphen token. Unknown verbs remain valid and reach the
	// server loop, including future extension verbs this package cannot know.
	ErrCommandVerbSyntax = errors.New("smtpwire: invalid command verb")
	// ErrCommandArgumentControl means an argument contained a control byte.
	ErrCommandArgumentControl = errors.New("smtpwire: command argument contains a control byte")
)

// Command is one decoded SMTP command line. Verb preserves the peer's exact
// spelling; command matching is case-insensitive at the session layer.
// Argument is the exact remainder after the separating run of SP bytes.
// It is empty when the command carried no argument.
type Command struct {
	Verb     string
	Argument string
}

// ReadCommand reads exactly one CRLF-terminated command. The configured bound
// includes CRLF and is enforced before allocation grows beyond it. A clean EOF
// before any byte is returned as io.EOF; EOF after a partial line is
// io.ErrUnexpectedEOF.
func (lr *LineReader) ReadCommand(deadline time.Time, limits Limits) (Command, error) {
	limits = limits.withDefaults()
	if err := lr.setDeadline(deadline); err != nil {
		return Command{}, err
	}

	line, err := lr.readCommandLine(limits.MaxCommandLineLength)
	if err != nil {
		return Command{}, err
	}
	if len(line) == 0 {
		return Command{}, ErrCommandEmpty
	}

	sep := strings.IndexByte(string(line), ' ')
	verb := string(line)
	argument := ""
	if sep >= 0 {
		verb = string(line[:sep])
		argument = strings.TrimLeft(string(line[sep+1:]), " ")
	}
	if err := validateCommandVerb(verb); err != nil {
		return Command{}, err
	}
	for i := 0; i < len(argument); i++ {
		if argument[i] < 0x20 || argument[i] == 0x7f {
			return Command{}, fmt.Errorf("%w: byte 0x%02x at offset %d", ErrCommandArgumentControl, argument[i], i)
		}
	}
	return Command{Verb: verb, Argument: argument}, nil
}

// ReadSASLResponse reads one strict CRLF-terminated RFC 4954 client response.
// Unlike ReadCommand, the whole line is opaque base64 data (or the abort token
// "*") and therefore is not subjected to command-verb syntax validation.
func (lr *LineReader) ReadSASLResponse(deadline time.Time, limits Limits) (string, error) {
	limits = limits.withDefaults()
	if err := lr.setDeadline(deadline); err != nil {
		return "", err
	}
	line, err := lr.readCommandLine(limits.MaxSASLResponseLength)
	if err != nil {
		return "", err
	}
	for i, b := range line {
		if b < 0x20 || b == 0x7f {
			return "", fmt.Errorf("%w: byte 0x%02x at offset %d", ErrCommandArgumentControl, b, i)
		}
	}
	return string(line), nil
}

func (lr *LineReader) readCommandLine(max int) ([]byte, error) {
	line := make([]byte, 0, min(max, 128))
	for consumed := 0; ; consumed++ {
		b, err := lr.br.ReadByte()
		if err != nil {
			if len(line) == 0 {
				return nil, err
			}
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if consumed+1 > max {
			// Consume the rest of an oversized line before returning. The
			// server normally closes after this framing failure, but AUTH owes
			// a 500 reply and a peer may still be blocked writing the tail.
			// Draining under the existing deadline prevents a write/write
			// deadlock without allocating in proportion to attacker input.
			for b != '\n' {
				b, err = lr.br.ReadByte()
				if err != nil {
					return nil, err
				}
			}
			return nil, ErrCommandLineTooLong
		}
		if b != '\n' {
			line = append(line, b)
			continue
		}
		if len(line) == 0 || line[len(line)-1] != '\r' {
			return nil, ErrCommandBareLF
		}
		return line[:len(line)-1], nil
	}
}

func validateCommandVerb(verb string) error {
	if verb == "" {
		return ErrCommandVerbSyntax
	}
	for i := 0; i < len(verb); i++ {
		c := verb[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%w: %q", ErrCommandVerbSyntax, verb)
		}
	}
	return nil
}
