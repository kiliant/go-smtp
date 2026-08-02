package saslprep

import "testing"

func TestPrepareMappingsAndBidi(t *testing.T) {
	got, err := Prepare("I\u00ADX\u00A0")
	if err != nil || got != "IX " {
		t.Fatalf("Prepare = %q, %v", got, err)
	}
	if _, err := Prepare("a\u05d0"); err != ErrBidi {
		t.Fatalf("bidi error = %v", err)
	}
}
