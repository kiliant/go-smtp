package harness

import "testing"

func TestRedactAuthLine(t *testing.T) {
	got := Redact("AUTH PLAIN AGludGVyb3AAaW50ZXJvcC1wdw==")
	if got != "AUTH PLAIN [REDACTED]" {
		t.Errorf("Redact(AUTH PLAIN ...) = %q", got)
	}
}

func TestRedactPasswordField(t *testing.T) {
	cases := []string{
		"password: interop-pw",
		"pw=interop-pw",
		"Password:interop-pw",
	}
	for _, c := range cases {
		got := Redact(c)
		if got == c {
			t.Errorf("Redact(%q) did not redact anything", c)
		}
		if got == "" {
			t.Errorf("Redact(%q) produced empty output", c)
		}
		if contains2(got, "interop-pw") {
			t.Errorf("Redact(%q) = %q, still contains the secret", c, got)
		}
	}
}

func contains2(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTranscriptRecordRedacts(t *testing.T) {
	tr := NewTranscript()
	tr.Record("AUTH PLAIN c2VjcmV0")
	tr.Recordf("password: %s", "hunter2")
	for _, line := range tr.Lines() {
		if contains2(line, "c2VjcmV0") || contains2(line, "hunter2") {
			t.Errorf("transcript line leaked a secret: %q", line)
		}
	}
}

func TestTranscriptNilSafe(t *testing.T) {
	var tr *Transcript
	tr.Record("should not panic")
	if tr.String() != "" {
		t.Error("nil transcript String() should be empty")
	}
	if tr.Lines() != nil {
		t.Error("nil transcript Lines() should be nil")
	}
}
