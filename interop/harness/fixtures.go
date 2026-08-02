package harness

import "strings"

// Fixture is one message body chosen to fail loudly against a specific bug
// class rather than merely exercising the happy path. See docs/INTEROP.md's
// fixture table, which this mirrors.
type Fixture struct {
	// Name identifies the fixture in test output.
	Name string
	// BugClass documents what a mismatch here would mean.
	BugClass string
	// Body is the message content the harness would hand to the client's
	// DATA phase, CRLF-terminated per line as RFC 5321 requires on the
	// wire. It does not include the RFC 5322 header block; callers compose
	// header + Body as needed for a given scenario.
	Body []byte
	// RequiresExtension is empty for fixtures every RFC 5321 server must
	// accept, or the extension a server must advertise before this fixture
	// is meaningful (e.g. BINARYMIME for the embedded-NUL case). A profile
	// missing it means the fixture's test skips against that server rather
	// than failing.
	RequiresExtension string
	// MinBodySize, when nonzero, is asserted against len(Body) so a fixture
	// intended to be large (the streaming case) cannot silently regress to
	// a small one.
	MinBodySize int

	_ struct{}
}

func repeatLine(fill byte, wireLength int) []byte {
	// RFC 5321 §4.5.3.1.6 counts the terminating CRLF in the 1000-octet
	// text-line limit. Keep the argument in wire octets so the boundary
	// fixture cannot accidentally describe a 1002-octet line as 1000.
	line := make([]byte, wireLength)
	for i := range wireLength - 2 {
		line[i] = fill
	}
	line[wireLength-2] = '\r'
	line[wireLength-1] = '\n'
	return line
}

// Fixtures is the fixed table of interop message bodies. Each targets one
// bug class from docs/INTEROP.md; do not add a happy-path-only variant here
// without a corresponding argument for what it would catch that another
// fixture does not.
var Fixtures = buildFixtures()

func buildFixtures() []Fixture {
	crlf := "\r\n"
	plain := "This is a plain 7-bit ASCII body.\r\nSecond line.\r\n"

	dotLine := "Before the lone dot.\r\n.\r\nAfter the lone dot.\r\n"

	dotDotLine := "Before.\r\n..A line that starts with two dots.\r\nAfter.\r\n"

	noTrailingCRLF := "This body does not end in CRLF." // deliberately no terminator

	line1000 := repeatLine('a', 1000)
	line1001 := repeatLine('b', 1001)
	var lineLimits []byte
	lineLimits = append(lineLimits, []byte("Line length boundary case.\r\n")...)
	lineLimits = append(lineLimits, line1000...)
	lineLimits = append(lineLimits, line1001...)

	eightBit := "Body with 8-bit octets: caf\xc3\xa9, na\xc3\xafve.\r\n"
	smtpUTF8 := "SMTPUTF8 envelope-path coupling fixture.\r\n"
	multiRecipient := "Multi-recipient acceptance fixture.\r\n"

	binaryNUL := "Binary body with an embedded NUL:\x00 right there.\r\n"

	var streaming strings.Builder
	const target = 200 * 1024 * 1024
	chunk := strings.Repeat("x", 998) + crlf
	for streaming.Len() < target {
		streaming.WriteString(chunk)
	}

	return []Fixture{
		{
			Name:     "plain-ascii",
			BugClass: "baseline happy path",
			Body:     []byte(plain),
		},
		{
			Name:     "dot-stuffing",
			BugClass: "dot-stuffing on send: a body line of exactly \".\" must not be read as end-of-data",
			Body:     []byte(dotLine),
		},
		{
			Name:     "dot-dot-unstuffing-symmetry",
			BugClass: "a line beginning \"..\" must round-trip to \"..\", not be un-stuffed twice",
			Body:     []byte(dotDotLine),
		},
		{
			Name:     "no-trailing-crlf",
			BugClass: "content not ending in CRLF: the DATA terminator boundary case",
			Body:     []byte(noTrailingCRLF),
		},
		{
			Name:        "line-length-1000-1001",
			BugClass:    "RFC 5321 §4.5.3.1.6 text-line limit: exactly 1000 octets must pass, 1001 exercises the boundary",
			Body:        lineLimits,
			MinBodySize: 1000 + 1001,
		},
		{
			Name:              "eight-bit-body",
			BugClass:          "8BITMIME vs 7-bit downgrade behaviour",
			Body:              []byte(eightBit),
			RequiresExtension: "8BITMIME",
		},
		{
			Name:              "smtp-utf8-recipient",
			BugClass:          "SMTPUTF8 must be requested on MAIL before a UTF-8 RCPT path is sent",
			Body:              []byte(smtpUTF8),
			RequiresExtension: "SMTPUTF8",
		},
		{
			Name:     "multi-recipient-one-invalid",
			BugClass: "pipelined RCPT results must remain associated with the right recipient",
			Body:     []byte(multiRecipient),
		},
		{
			Name:              "binary-with-nul",
			BugClass:          "BINARYMIME + CHUNKING: the only legal way to send an embedded NUL",
			Body:              []byte(binaryNUL),
			RequiresExtension: "BINARYMIME",
		},
		{
			Name:        "streaming-200mib",
			BugClass:    "streaming guarantee: peak allocation must stay flat for a 200 MiB message",
			Body:        []byte(streaming.String()),
			MinBodySize: target,
		},
	}
}

// FixtureByName returns the fixture with the given name, for scenarios that
// select one out of the table explicitly.
func FixtureByName(name string) (Fixture, bool) {
	for _, f := range Fixtures {
		if f.Name == name {
			return f, true
		}
	}
	return Fixture{}, false
}
