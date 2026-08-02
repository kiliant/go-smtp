package smtpclient

import "testing"

func TestParseLimitsParam(t *testing.T) {
	got, err := ParseLimitsParam("RCPTMAX=20 MAILMAX=5 RCPTDOMAINMAX=3 FUTURE=word")
	if err != nil {
		t.Fatal(err)
	}
	if got.RcptMax != 20 || got.MailMax != 5 || got.RcptDomainMax != 3 {
		t.Fatalf("ParseLimitsParam = %#v", got)
	}
	got, err = ParseLimitsParam("RCPTMAX=000")
	if err != nil || got.RcptMax != 0 {
		t.Fatalf("invalid registered limit = %#v, %v", got, err)
	}
	if _, err := ParseLimitsParam("RCPTMAX"); err == nil {
		t.Fatal("missing equals accepted")
	}
}
