package smtpwire

import (
	"errors"
	"strings"
)

// Extension is one line of an EHLO reply after the greeting line: a keyword
// plus its space-separated parameters, per RFC 5321 §4.1.1.1.
//
//	ehlo-line = ehlo-keyword *( SP ehlo-param )
type Extension struct {
	// Keyword is normalised to upper case; keywords are case-insensitive
	// on the wire (RFC 5321 §4.1.1.1). A keyword this library has never
	// heard of is returned exactly like any other — dropping it is data
	// loss a caller cannot recover from.
	Keyword string
	// Params holds each ehlo-param verbatim, in order, split on runs of
	// whitespace. Nil (not empty-non-nil) when the keyword had no
	// parameters.
	Params []string
	// Raw is everything after the keyword and its separating space,
	// completely unparsed — the exact bytes the server sent, for callers
	// that need more fidelity than whitespace-split Params gives (e.g. a
	// parameter syntax that is meaningfully space-sensitive). Empty when
	// the keyword had no parameters.
	Raw string
}

// EHLOReply is a fully parsed EHLO reply.
type EHLOReply struct {
	// Domain is the server's greeting domain or address-literal from the
	// first line — never an extension keyword. Confusing the two is the
	// single most common bug in hand-written EHLO parsers.
	Domain string
	// Greeting is any free text on the first line after Domain, per
	// RFC 5321's [ SP greeting-text ]. Empty if absent.
	Greeting string
	// Extensions holds every subsequent line, in order.
	Extensions []Extension
}

// ErrEHLOEmpty is returned when there are no lines to parse at all — the
// caller passed an empty reply.
var ErrEHLOEmpty = errors.New("smtpwire: EHLO reply has no lines")

// ErrEHLOEmptyKeyword is returned when a non-greeting line has no keyword —
// e.g. a blank continuation line, which the grammar does not permit.
var ErrEHLOEmptyKeyword = errors.New("smtpwire: EHLO extension line has no keyword")

// ParseEHLOReply parses the already-framed lines of an EHLO reply (as
// produced by Reply.Lines from ReadReply — this function does not reframe
// or re-validate the reply code).
//
// The first line is always the greeting domain, never a keyword — treating
// it as one silently registers a bogus extension named after the server's
// hostname, which is the specific, recurring bug this parser exists to
// avoid. Every subsequent line is one Extension.
//
// Keyword character validation is deliberately lenient: any non-whitespace
// token in keyword position is accepted and upper-cased, rather than
// rejected against the strict (ALPHA/DIGIT)*(ALPHA/DIGIT/"-") grammar. This
// is a data-capture parser, not a protocol gate — a slightly malformed but
// harmless keyword from a real server should still reach the caller rather
// than aborting EHLO parsing entirely.
func ParseEHLOReply(lines []string) (EHLOReply, error) {
	if len(lines) == 0 {
		return EHLOReply{}, ErrEHLOEmpty
	}

	domain, greeting, _ := strings.Cut(lines[0], " ")
	reply := EHLOReply{Domain: domain, Greeting: greeting}

	if len(lines) == 1 {
		return reply, nil
	}
	reply.Extensions = make([]Extension, 0, len(lines)-1)
	for _, line := range lines[1:] {
		kw, raw, found := strings.Cut(line, " ")
		if kw == "" {
			return EHLOReply{}, ErrEHLOEmptyKeyword
		}
		ext := Extension{Keyword: strings.ToUpper(kw)}
		if found {
			ext.Raw = raw
			if fields := strings.Fields(raw); len(fields) > 0 {
				ext.Params = fields
			}
		}
		reply.Extensions = append(reply.Extensions, ext)
	}
	return reply, nil
}
