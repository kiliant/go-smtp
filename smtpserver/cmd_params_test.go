package smtpserver

import (
	"errors"
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestParseMailParametersFloorAndUnknown(t *testing.T) {
	opts, err := parseMailParameters([]smtpwire.Param{
		{Keyword: "size", Value: "123"},
		{Keyword: "BoDy", Value: "8bitmime"},
		{Keyword: "sMtPuTf8"},
		{Keyword: "AUTH", Value: "+3C+3E"},
		{Keyword: "X-Future", Value: "Opaque"},
	}, mailParameterFeatures{size: true, eightBit: true, smtpUTF8: true, auth: true})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Transport == nil || opts.Transport.Size == nil || *opts.Transport.Size != 123 {
		t.Fatalf("SIZE was not parsed: %+v", opts)
	}
	if opts.Transport.Body != smtp.BodyType8BitMIME || !opts.Transport.SMTPUTF8 {
		t.Fatalf("transport options = %+v", opts.Transport)
	}
	if opts.Auth != "<>" {
		t.Fatalf("AUTH = %q, want decoded <>", opts.Auth)
	}
	if opts.AuthOriginal == nil || *opts.AuthOriginal != (smtp.Param{Keyword: "AUTH", Value: "+3C+3E"}) {
		t.Fatalf("AUTH original = %+v", opts.AuthOriginal)
	}
	if len(opts.Extra) != 1 || opts.Extra[0].Keyword != "X-Future" || opts.Extra[0].Value != "Opaque" {
		t.Fatalf("Extra = %+v", opts.Extra)
	}
}

func TestParseMailParametersRejectsMalformedOrUnadvertisedKnownValues(t *testing.T) {
	tests := []struct {
		name     string
		params   []smtpwire.Param
		features mailParameterFeatures
		keyword  string
	}{
		{name: "size syntax", params: []smtpwire.Param{{Keyword: "SIZE", Value: "-1"}}, features: mailParameterFeatures{size: true}, keyword: "SIZE"},
		{name: "body syntax", params: []smtpwire.Param{{Keyword: "BODY", Value: "FUTURE"}}, features: mailParameterFeatures{eightBit: true}, keyword: "BODY"},
		{name: "binary unadvertised", params: []smtpwire.Param{{Keyword: "BODY", Value: "BINARYMIME"}}, features: mailParameterFeatures{eightBit: true}, keyword: "BODY"},
		{name: "smtputf8 value", params: []smtpwire.Param{{Keyword: "SMTPUTF8", Value: "yes"}}, features: mailParameterFeatures{smtpUTF8: true}, keyword: "SMTPUTF8"},
		{name: "auth xtext", params: []smtpwire.Param{{Keyword: "AUTH", Value: "+ZZ"}}, features: mailParameterFeatures{auth: true}, keyword: "AUTH"},
		{name: "duplicate", params: []smtpwire.Param{{Keyword: "SIZE", Value: "1"}, {Keyword: "size", Value: "2"}}, features: mailParameterFeatures{size: true}, keyword: "SIZE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseMailParameters(test.params, test.features)
			if !errors.Is(err, errCommandParameter) {
				t.Fatalf("error = %v, want command parameter error", err)
			}
			var parameter *parameterError
			if !errors.As(err, &parameter) || parameter.keyword != test.keyword {
				t.Fatalf("error = %v, want keyword %s", err, test.keyword)
			}
		})
	}
}

func TestParseRcptParametersPreservesUnknownSpellingAndOrder(t *testing.T) {
	opts := parseRcptParameters([]smtpwire.Param{{Keyword: "X-One", Value: "a"}, {Keyword: "x-two"}})
	if opts == nil || len(opts.Extra) != 2 || opts.Extra[0].Keyword != "X-One" || opts.Extra[1].Keyword != "x-two" {
		t.Fatalf("RCPT options = %+v", opts)
	}
}
