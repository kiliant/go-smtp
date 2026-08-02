package smtp

import "testing"

// TestDataResultSMTPSingleElement stresses the SMTP case named in
// docs/API-STABILITY.md §8: one reply for the whole message, modelled as a
// one-element DataResult where every recipient carries the identical reply.
func TestDataResultSMTPSingleElement(t *testing.T) {
	reply := RecipientResult{Recipient: "a@example.com", Command: "DATA", Code: 250, Text: "OK"}
	reply2 := RecipientResult{Recipient: "b@example.com", Command: "DATA", Code: 250, Text: "OK"}
	dr := DataResult{reply, reply2}

	if !dr.AllAccepted() {
		t.Errorf("AllAccepted() = false, want true")
	}
	if errs := dr.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
}

// TestDataResultLMTPPerRecipient stresses the named future extension this
// task exists to protect: LMTP (RFC 2033 §4.2) returning one reply per
// recipient, some accepted and some rejected, from the very type SMTP also
// uses. A "reply to DATA" type shaped for the single-message case could not
// express this without changing its return type — this test is the
// regression guard for that.
func TestDataResultLMTPPerRecipient(t *testing.T) {
	dr := DataResult{
		{Recipient: "ok@example.com", Command: "DATA", Code: 250, Enhanced: ParseEnhancedCode("2.1.5"), Text: "2.1.5 OK"},
		{Recipient: "full@example.com", Command: "DATA", Code: 452, Enhanced: ParseEnhancedCode("4.2.2"), Text: "4.2.2 mailbox full"},
		{Recipient: "unknown@example.com", Command: "DATA", Code: 550, Enhanced: ParseEnhancedCode("5.1.1"), Text: "5.1.1 no such user"},
	}

	if dr.AllAccepted() {
		t.Errorf("AllAccepted() = true, want false")
	}

	errs := dr.Errors()
	if len(errs) != 2 {
		t.Fatalf("Errors() has %d entries, want 2", len(errs))
	}
	if errs[0].Code != 452 || !errs[0].IsTransient() {
		t.Errorf("errs[0] = %+v, want transient 452", errs[0])
	}
	if errs[1].Code != 550 || !errs[1].IsPermanent() {
		t.Errorf("errs[1] = %+v, want permanent 550", errs[1])
	}

	if !dr[0].Accepted() {
		t.Errorf("dr[0].Accepted() = false, want true")
	}
	if dr[0].Err() != nil {
		t.Errorf("dr[0].Err() = %v, want nil", dr[0].Err())
	}
}

func TestDataResultEmptyNotAccepted(t *testing.T) {
	var dr DataResult
	if dr.AllAccepted() {
		t.Errorf("AllAccepted() on empty DataResult = true, want false")
	}
	if errs := dr.Errors(); errs == nil || len(errs) != 0 {
		t.Errorf("Errors() on empty DataResult = %v, want empty non-nil slice", errs)
	}
}

func TestRecipientResultAccepted(t *testing.T) {
	tests := []struct {
		code     int
		accepted bool
	}{
		{250, true},
		{251, true},
		{200, true},
		{299, true},
		{354, false},
		{421, false},
		{550, false},
		{0, false},
	}
	for _, tt := range tests {
		r := RecipientResult{Code: tt.code}
		if got := r.Accepted(); got != tt.accepted {
			t.Errorf("RecipientResult{Code:%d}.Accepted() = %v, want %v", tt.code, got, tt.accepted)
		}
	}
}
