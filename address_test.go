package smtp

import "testing"

func TestPathLengthConstants(t *testing.T) {
	// RFC 5321 §4.5.3.1: local-part 64, domain 255, path 256. Checked
	// against docs/RFC-COVERAGE.md and .state/status.md's verified-facts
	// section, not recalled from memory.
	if MinLocalPartLength != 64 {
		t.Errorf("MinLocalPartLength = %d, want 64", MinLocalPartLength)
	}
	if MinDomainLength != 255 {
		t.Errorf("MinDomainLength = %d, want 255", MinDomainLength)
	}
	if MinPathLength != 256 {
		t.Errorf("MinPathLength = %d, want 256", MinPathLength)
	}
}
