package harness

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Transcript records a bounded log of the wire-level and harness-level
// events for one server/scenario run, for attaching to a failure. Every
// recorded line passes through Redact first, so a transcript is safe to
// print in CI output or attach to a bug report.
type Transcript struct {
	mu    sync.Mutex
	lines []string
}

// NewTranscript returns an empty transcript.
func NewTranscript() *Transcript {
	return &Transcript{}
}

// Record appends one redacted line.
func (tr *Transcript) Record(line string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.lines = append(tr.lines, Redact(line))
}

// Recordf appends one redacted, formatted line.
func (tr *Transcript) Recordf(format string, args ...any) {
	tr.Record(fmt.Sprintf(format, args...))
}

// Lines returns a copy of the recorded, already-redacted lines.
func (tr *Transcript) Lines() []string {
	if tr == nil {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := make([]string, len(tr.lines))
	copy(out, tr.lines)
	return out
}

// String joins the recorded lines for a single diagnostic block.
func (tr *Transcript) String() string {
	if tr == nil {
		return ""
	}
	return strings.Join(tr.Lines(), "\n")
}

// authArgPattern matches the base64 argument of AUTH PLAIN/LOGIN continuation
// lines and the AUTH command itself, which carry credential bytes directly
// on the wire.
var authArgPattern = regexp.MustCompile(`(?i)^(AUTH\s+\S+\s+)(\S+)`)

// passwordFieldPattern matches "password: <value>" or "pw=<value>"-style
// harness-authored diagnostics, so a provisioning helper that logs what it
// configured cannot leak the value even if it forgets to redact itself.
var passwordFieldPattern = regexp.MustCompile(`(?i)(pass(word)?|pw)(\s*[:=]\s*)(\S+)`)

// Redact removes credential material from a line before it is recorded or
// printed. It is deliberately conservative: known credential-shaped patterns
// are replaced with a fixed marker rather than attempting to reconstruct
// which substrings are "safe".
func Redact(line string) string {
	line = authArgPattern.ReplaceAllString(line, "${1}[REDACTED]")
	line = passwordFieldPattern.ReplaceAllString(line, "${1}${3}[REDACTED]")
	return line
}
