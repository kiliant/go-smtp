package smtpserver

import (
	"errors"
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestParseExtensionMailParameters(t *testing.T) {
	extensions := map[smtp.Extension]string{
		smtp.ExtDSN:           "",
		smtp.ExtDeliverBy:     "30",
		smtp.ExtFutureRelease: "3600 2030-01-01T00:00:00Z",
		smtp.ExtMTPriority:    "",
		smtp.ExtRequireTLS:    "",
		smtp.ExtNoSoliciting:  "",
		smtp.ExtMTRK:          "",
		smtp.ExtSubmitter:     "",
		smtp.ExtConPerm:       "",
	}
	opts, err := parseMailParameters([]smtpwire.Param{
		{Keyword: "RET", Value: "future-value"},
		{Keyword: "EnViD", Value: "+41"},
		{Keyword: "BY", Value: "+60;RT"},
		{Keyword: "HOLDFOR", Value: "120"},
		{Keyword: "MT-PRIORITY", Value: "42"},
		{Keyword: "REQUIRETLS"},
		{Keyword: "SOLICIT", Value: "bulk:mail,notice"},
		{Keyword: "MTRK", Value: "YWJjMTIz:86400"},
		{Keyword: "SUBMITTER", Value: "+75ser@example.test"},
		{Keyword: "CONPERM"},
	}, mailParameterFeatures{extensions: extensions})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Delivery == nil || opts.Delivery.DSN == nil || opts.Delivery.DSN.Return != smtp.DSNReturn("FUTURE-VALUE") {
		t.Fatalf("DSN = %+v", opts.Delivery)
	}
	if opts.Delivery.DSN.EnvelopeID != "A" || opts.Delivery.DSN.EnvelopeIDOriginal == nil || opts.Delivery.DSN.EnvelopeIDOriginal.Value != "+41" {
		t.Fatalf("ENVID = %+v", opts.Delivery.DSN)
	}
	if opts.Delivery.DeliverBy == nil || opts.Delivery.DeliverBy.Seconds != 60 || opts.Delivery.DeliverBy.Mode != "R" || !opts.Delivery.DeliverBy.Trace {
		t.Fatalf("DELIVERBY = %+v", opts.Delivery.DeliverBy)
	}
	if opts.Delivery.FutureRelease == nil || opts.Delivery.FutureRelease.HoldForSeconds != 120 {
		t.Fatalf("FUTURERELEASE = %+v", opts.Delivery.FutureRelease)
	}
	if opts.Delivery.MTPriority != smtp.MTPriority("42") || !opts.Delivery.RequireTLS {
		t.Fatalf("delivery options = %+v", opts.Delivery)
	}
	if opts.Legacy == nil || opts.Legacy.Solicit != "bulk:mail,notice" || opts.Legacy.TransitID != "YWJjMTIz:86400" || opts.Legacy.Submitter != "user@example.test" || !opts.Legacy.ConPerm {
		t.Fatalf("legacy options = %+v", opts.Legacy)
	}
	if opts.Legacy.SubmitterOriginal == nil || opts.Legacy.SubmitterOriginal.Value != "+75ser@example.test" {
		t.Fatalf("SUBMITTER original = %+v", opts.Legacy.SubmitterOriginal)
	}
}

func TestParseDeliverByRejectsSeparatedTraceModifier(t *testing.T) {
	if _, err := parseDeliverBy("60;R;T", "30"); err == nil {
		t.Fatal("parseDeliverBy accepted non-RFC trace spelling")
	}
}

func TestParseExtensionRcptParameters(t *testing.T) {
	extensions := map[smtp.Extension]string{
		smtp.ExtDSN:    "",
		smtp.ExtRRVS:   "",
		smtp.ExtConNeg: "",
	}
	opts, err := parseRcptParameters([]smtpwire.Param{
		{Keyword: "NOTIFY", Value: "success,FUTURE"},
		{Keyword: "OrCpT", Value: "utf-8;+41"},
		{Keyword: "RRVS", Value: "2026-08-14T10:00:00+02:00;R"},
		{Keyword: "CONNEG"},
	}, extensions)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Delivery == nil || opts.Delivery.DSN == nil || len(opts.Delivery.DSN.Notify) != 2 || opts.Delivery.DSN.Notify[1] != smtp.DSNNotify("FUTURE") {
		t.Fatalf("NOTIFY = %+v", opts.Delivery)
	}
	if opts.Delivery.DSN.OriginalType != "utf-8" || opts.Delivery.DSN.Original != "A" || opts.Delivery.DSN.ORCPTOriginal == nil || opts.Delivery.DSN.ORCPTOriginal.Value != "utf-8;+41" {
		t.Fatalf("ORCPT = %+v", opts.Delivery.DSN)
	}
	if opts.Delivery.RRVS == nil || opts.Delivery.RRVS.Timestamp != "2026-08-14T08:00:00Z" || opts.Delivery.RRVS.Disposition != "R" {
		t.Fatalf("RRVS = %+v", opts.Delivery.RRVS)
	}
	if opts.Legacy == nil || !opts.Legacy.ConNeg {
		t.Fatalf("CONNEG = %+v", opts.Legacy)
	}
}

func TestKnownExtensionParametersRequireAdvertisement(t *testing.T) {
	mail := []string{"RET", "ENVID", "BY", "HOLDFOR", "HOLDUNTIL", "MT-PRIORITY", "REQUIRETLS", "SOLICIT", "MTRK", "SUBMITTER", "CONPERM"}
	for _, keyword := range mail {
		t.Run("MAIL/"+keyword, func(t *testing.T) {
			_, err := parseMailParameters([]smtpwire.Param{{Keyword: keyword, Value: extensionTestValue(keyword)}}, mailParameterFeatures{})
			assertParameterError(t, err, keyword)
		})
	}
	for _, keyword := range []string{"NOTIFY", "ORCPT", "RRVS", "CONNEG"} {
		t.Run("RCPT/"+keyword, func(t *testing.T) {
			_, err := parseRcptParameters([]smtpwire.Param{{Keyword: keyword, Value: extensionTestValue(keyword)}}, nil)
			assertParameterError(t, err, keyword)
		})
	}
}

func TestExtensionParameterSemanticErrors(t *testing.T) {
	mailExtensions := map[smtp.Extension]string{
		smtp.ExtDeliverBy:     "30",
		smtp.ExtFutureRelease: "3600 2030-01-01T00:00:00Z",
		smtp.ExtRequireTLS:    "",
	}
	for _, test := range []struct {
		keyword string
		value   string
	}{
		{keyword: "BY", value: "5;R"},
		{keyword: "BY", value: "5"},
		{keyword: "HOLDFOR", value: "0"},
		{keyword: "REQUIRETLS", value: "yes"},
	} {
		_, err := parseMailParameters([]smtpwire.Param{{Keyword: test.keyword, Value: test.value}}, mailParameterFeatures{extensions: mailExtensions})
		assertParameterError(t, err, test.keyword)
	}
	_, err := parseMailParameters([]smtpwire.Param{{Keyword: "HOLDFOR", Value: "1"}, {Keyword: "HOLDUNTIL", Value: "2029-01-01T00:00:00Z"}}, mailParameterFeatures{extensions: mailExtensions})
	assertParameterError(t, err, "HOLDUNTIL")

	rcptExtensions := map[smtp.Extension]string{smtp.ExtDSN: "", smtp.ExtRRVS: ""}
	for _, test := range []struct {
		keyword string
		value   string
	}{
		{keyword: "NOTIFY", value: "NEVER,FAILURE"},
		{keyword: "ORCPT", value: "rfc822;+ZZ"},
		{keyword: "RRVS", value: "not-a-time"},
	} {
		_, err := parseRcptParameters([]smtpwire.Param{{Keyword: test.keyword, Value: test.value}}, rcptExtensions)
		assertParameterError(t, err, test.keyword)
	}
}

func TestFutureReleaseRejectsNullReversePath(t *testing.T) {
	opts := &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{FutureRelease: &smtp.FutureReleaseOptions{HoldForSeconds: 1}}}
	assertParameterError(t, validateExtensionMailPath("", opts), "FUTURERELEASE")
}

func TestMTRKRequiresUniqueENVID(t *testing.T) {
	exts := map[smtp.Extension]string{smtp.ExtDSN: "", smtp.ExtMTRK: ""}
	opts, err := parseMailParameters([]smtpwire.Param{
		{Keyword: "ENVID", Value: "not-unique"},
		{Keyword: "MTRK", Value: "YWJjMTIz"},
	}, mailParameterFeatures{extensions: exts})
	if err != nil {
		t.Fatal(err)
	}
	assertParameterError(t, validateExtensionMailPath("sender@example.test", opts), "MTRK")
}

func TestParseRRVSRejectsFractionalSeconds(t *testing.T) {
	if _, err := parseRRVS("2026-08-14T10:00:00.5Z"); err == nil {
		t.Fatal("parseRRVS accepted forbidden time-secfrac")
	}
}

func assertParameterError(t *testing.T, err error, keyword string) {
	t.Helper()
	if !errors.Is(err, errCommandParameter) {
		t.Fatalf("error = %v, want command parameter error", err)
	}
	var parameter *parameterError
	if !errors.As(err, &parameter) || parameter.keyword != keyword {
		t.Fatalf("error = %v, want keyword %s", err, keyword)
	}
}

func extensionTestValue(keyword string) string {
	switch keyword {
	case "REQUIRETLS", "CONPERM", "CONNEG":
		return ""
	case "BY":
		return "60;N"
	case "HOLDFOR":
		return "60"
	case "HOLDUNTIL", "RRVS":
		return "2029-01-01T00:00:00Z"
	case "NOTIFY":
		return "FAILURE"
	case "ORCPT":
		return "rfc822;user@example.test"
	default:
		return "value"
	}
}
