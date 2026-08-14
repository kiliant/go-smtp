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
	if got.Extra != "FUTURE=word" {
		t.Fatalf("ParseLimitsParam Extra = %q", got.Extra)
	}
	got, err = ParseLimitsParam("RCPTMAX=000")
	if err != nil || got.RcptMax != 0 {
		t.Fatalf("invalid registered limit = %#v, %v", got, err)
	}
	got, err = ParseLimitsParam("RCPTMAX FUTURE_FLAG FUTURE_VALUE=word=value")
	if err != nil || got.RcptMax != 0 || got.Extra != "FUTURE_FLAG FUTURE_VALUE=word=value" {
		t.Fatalf("open limits = %#v, %v", got, err)
	}
	if _, err := ParseLimitsParam("FUTURE=bad;value"); err == nil {
		t.Fatal("semicolon in limit value accepted")
	}
}
