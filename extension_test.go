package smtp

import "testing"

func TestExtensionConstantsMatchWireForm(t *testing.T) {
	// Spot-check that the constants carry the exact wire keyword, since a
	// server compares these (case-insensitively per RFC 5321) against
	// what it advertised.
	tests := []struct {
		ext  Extension
		want string
	}{
		{ExtStartTLS, "STARTTLS"},
		{ExtPipelining, "PIPELINING"},
		{ExtEnhancedStatusCodes, "ENHANCEDSTATUSCODES"},
		{ExtAuth, "AUTH"},
		{ExtSize, "SIZE"},
		{Ext8BitMIME, "8BITMIME"},
		{ExtSMTPUTF8, "SMTPUTF8"},
		{ExtBinaryMIME, "BINARYMIME"},
		{ExtChunking, "CHUNKING"},
		{ExtUTF8SMTP, "UTF8SMTP"},
		{ExtDSN, "DSN"},
		{ExtFutureRelease, "FUTURERELEASE"},
		{ExtMTPriority, "MT-PRIORITY"},
		{ExtRequireTLS, "REQUIRETLS"},
		{ExtLimits, "LIMITS"},
		{ExtNoSoliciting, "NO-SOLICITING"},
		{ExtTurn, "TURN"},
	}
	for _, tt := range tests {
		if string(tt.ext) != tt.want {
			t.Errorf("Extension constant = %q, want %q", string(tt.ext), tt.want)
		}
	}
}

// TestExtensionPreservesUnknownKeyword stresses the direction rule from
// docs/API-STABILITY.md §1a directly: an EHLO keyword this library has
// never named — the IANA registry's next entry after LIMITS (RFC 9422) —
// must still construct and compare as an Extension like any known one. A
// bool-per-extension design could not represent this at all.
func TestExtensionPreservesUnknownKeyword(t *testing.T) {
	future := Extension("SOME-FUTURE-EXTENSION")
	if string(future) != "SOME-FUTURE-EXTENSION" {
		t.Errorf("Extension(%q) round-trip failed", future)
	}
	if future == ExtLimits {
		t.Errorf("unknown extension must not alias a known constant")
	}
}
