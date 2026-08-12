package smtpserver

import (
	"reflect"
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestComputeCapabilitiesFiltersEveryDescriptorField(t *testing.T) {
	const (
		backendMail backendFeatureSet = 1 << iota
		backendAuth
	)
	descriptors := []capabilityDescriptor{
		{keyword: smtp.Extension("ALWAYS"), modes: modeSetBoth},
		{keyword: smtp.Extension("SMTP-ONLY"), modes: modeSetSMTP},
		{keyword: smtp.Extension("LMTP-ONLY"), modes: modeSetLMTP},
		{keyword: smtp.Extension("CLEAR-ONLY"), modes: modeSetBoth, requiresTLS: tlsBefore},
		{keyword: smtp.Extension("TLS-ONLY"), modes: modeSetBoth, requiresTLS: tlsAfter},
		{keyword: smtp.Extension("AUTHED"), modes: modeSetBoth, requiresAuth: true},
		{keyword: smtp.Extension("BACKEND"), modes: modeSetBoth, requiresBackend: backendMail | backendAuth},
		{
			keyword:   smtp.Extension("DYNAMIC"),
			modes:     modeSetBoth,
			available: func(ctx capabilityContext) bool { return ctx.tls && ctx.authenticated },
		},
		{
			keyword: smtp.Extension("PARAMS"),
			modes:   modeSetBoth,
			params:  func(capabilityContext) string { return "one two" },
		},
	}

	tests := []struct {
		name string
		ctx  capabilityContext
		want []smtpwire.Extension
	}{
		{
			name: "clear SMTP partial backend",
			ctx:  capabilityContext{mode: modeSMTP, backend: backendMail},
			want: []smtpwire.Extension{
				{Keyword: "ALWAYS"},
				{Keyword: "SMTP-ONLY"},
				{Keyword: "CLEAR-ONLY"},
				{Keyword: "PARAMS", Raw: "one two"},
			},
		},
		{
			name: "TLS authenticated LMTP full backend",
			ctx: capabilityContext{
				mode:          modeLMTP,
				tls:           true,
				authenticated: true,
				backend:       backendMail | backendAuth,
			},
			want: []smtpwire.Extension{
				{Keyword: "ALWAYS"},
				{Keyword: "LMTP-ONLY"},
				{Keyword: "TLS-ONLY"},
				{Keyword: "AUTHED"},
				{Keyword: "BACKEND"},
				{Keyword: "DYNAMIC"},
				{Keyword: "PARAMS", Raw: "one two"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := computeCapabilities(test.ctx, descriptors); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("capabilities = %#v, want %#v", got, test.want)
			}
		})
	}
}
