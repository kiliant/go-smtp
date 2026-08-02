package smtpclient

import (
	"errors"
	"fmt"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

func init() {
	registerMailExtension("legacy", (*Client).legacyMailParams)
	registerRcptExtension("legacy", (*Client).legacyRcptParams)
}

func (c *Client) legacyMailParams(_ string, opts *smtp.MailOptions) ([]smtp.Param, error) {
	if opts == nil || opts.Legacy == nil {
		return nil, nil
	}
	l := opts.Legacy
	params := make([]smtp.Param, 0, 4)
	if l.Solicit != "" {
		if !validSolicit(l.Solicit) {
			return nil, fmt.Errorf("smtpclient: invalid SOLICIT value %q", l.Solicit)
		}
		if !c.advertises(string(smtp.ExtNoSoliciting)) {
			return nil, missingExtension(smtp.ExtNoSoliciting)
		}
		params = append(params, smtp.Param{Keyword: "SOLICIT", Value: l.Solicit})
	}
	if l.TransitID != "" {
		if !validESMTPValue(l.TransitID) {
			return nil, fmt.Errorf("smtpclient: invalid TRANSID value %q", l.TransitID)
		}
		if !c.advertises(string(smtp.ExtMTRK)) {
			return nil, missingExtension(smtp.ExtMTRK)
		}
		params = append(params, smtp.Param{Keyword: "TRANSID", Value: l.TransitID})
	}
	if l.Submitter != "" {
		if strings.ContainsAny(l.Submitter, "\r\n\x00") {
			return nil, errors.New("smtpclient: SUBMITTER contains SMTP command framing")
		}
		if !c.advertises(string(smtp.ExtSubmitter)) {
			return nil, missingExtension(smtp.ExtSubmitter)
		}
		params = append(params, smtp.Param{Keyword: "SUBMITTER", Value: smtp.EncodeXtext(l.Submitter)})
	}
	if l.ConPerm {
		if !c.advertises(string(smtp.ExtConPerm)) {
			return nil, missingExtension(smtp.ExtConPerm)
		}
		params = append(params, smtp.Param{Keyword: "CONPERM"})
	}
	return params, nil
}

func (c *Client) legacyRcptParams(_ string, opts *smtp.RcptOptions) ([]smtp.Param, error) {
	if opts == nil || opts.Legacy == nil || !opts.Legacy.ConNeg {
		return nil, nil
	}
	if !c.advertises(string(smtp.ExtConNeg)) {
		return nil, missingExtension(smtp.ExtConNeg)
	}
	return []smtp.Param{{Keyword: "CONNEG"}}, nil
}

func validSolicit(value string) bool {
	if value == "" || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, keyword := range strings.Split(value, ",") {
		if keyword == "" || !isSolicitKeyword(keyword) {
			return false
		}
	}
	return true
}

func isSolicitKeyword(keyword string) bool {
	for _, r := range keyword {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`{|}~:", r) {
			continue
		}
		return false
	}
	return true
}

func validESMTPValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, " \t\r\n\x00= ")
}
