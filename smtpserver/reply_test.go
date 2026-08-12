package smtpserver

import (
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestNormalizeEnhancedCodePreservesPrimaryClass(t *testing.T) {
	result := smtp.RecipientResult{
		Recipient: "user@example.test",
		Command:   "DATA",
		Code:      550,
		Enhanced:  smtp.ParseEnhancedCode("4.7.1"),
		Text:      "policy rejection",
	}
	got, repaired := normalizeRecipientResult(result)
	if !repaired {
		t.Fatal("class mismatch was not reported")
	}
	if got.Code != 550 || got.Enhanced.String() != "5.0.0" || got.Text != result.Text {
		t.Fatalf("normalized result = %+v", got)
	}
}

func TestNormalizeEnhancedCodeLeavesAgreementAndAbsenceAlone(t *testing.T) {
	for _, enhanced := range []smtp.EnhancedCode{
		{},
		smtp.ParseEnhancedCode("4.7.1"),
	} {
		got, repaired := normalizeEnhancedCode(451, enhanced)
		if repaired || got != enhanced {
			t.Fatalf("normalize(451, %+v) = (%+v, %v)", enhanced, got, repaired)
		}
	}
}
