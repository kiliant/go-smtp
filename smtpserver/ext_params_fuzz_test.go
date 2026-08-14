package smtpserver

import (
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func FuzzExtensionMailParameters(f *testing.F) {
	for _, seed := range []smtpwire.Param{
		{Keyword: "RET", Value: "FULL"},
		{Keyword: "ENVID", Value: "queue+2Bid"},
		{Keyword: "BY", Value: "+60;RT"},
		{Keyword: "HOLDFOR", Value: "60"},
		{Keyword: "HOLDUNTIL", Value: "2030-01-01T00:00:00Z"},
		{Keyword: "MT-PRIORITY", Value: "42"},
		{Keyword: "REQUIRETLS"},
		{Keyword: "SOLICIT", Value: "org.example:bulk"},
		{Keyword: "MTRK", Value: "YWJjMTIz:86400"},
		{Keyword: "SUBMITTER", Value: "user+40example.test"},
		{Keyword: "CONPERM"},
	} {
		f.Add(seed.Keyword, seed.Value)
	}

	extensions := fuzzParameterExtensions()
	f.Fuzz(func(t *testing.T, keyword, value string) {
		if len(keyword)+len(value) > 4096 {
			return
		}
		_, _ = parseMailParameters([]smtpwire.Param{{Keyword: keyword, Value: value}}, mailParameterFeatures{extensions: extensions})
	})
}

func FuzzExtensionRcptParameters(f *testing.F) {
	for _, seed := range []smtpwire.Param{
		{Keyword: "NOTIFY", Value: "SUCCESS,FAILURE"},
		{Keyword: "ORCPT", Value: "rfc822;user+40example.test"},
		{Keyword: "RRVS", Value: "2030-01-01T00:00:00Z;C"},
		{Keyword: "CONNEG"},
	} {
		f.Add(seed.Keyword, seed.Value)
	}

	extensions := fuzzParameterExtensions()
	f.Fuzz(func(t *testing.T, keyword, value string) {
		if len(keyword)+len(value) > 4096 {
			return
		}
		_, _ = parseRcptParameters([]smtpwire.Param{{Keyword: keyword, Value: value}}, extensions)
	})
}

func fuzzParameterExtensions() map[smtp.Extension]string {
	return map[smtp.Extension]string{
		smtp.ExtDSN:           "",
		smtp.ExtDeliverBy:     "30",
		smtp.ExtFutureRelease: "999999999 2038-01-19T03:14:07Z",
		smtp.ExtMTPriority:    "",
		smtp.ExtRequireTLS:    "",
		smtp.ExtNoSoliciting:  "",
		smtp.ExtMTRK:          "",
		smtp.ExtSubmitter:     "",
		smtp.ExtConPerm:       "",
		smtp.ExtRRVS:          "",
		smtp.ExtConNeg:        "",
	}
}
