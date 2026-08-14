package smtpclient

import (
	"errors"
	"fmt"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
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
		if !validMTRK(l.TransitID) {
			return nil, fmt.Errorf("smtpclient: invalid MTRK value %q", l.TransitID)
		}
		if !c.advertises(string(smtp.ExtMTRK)) {
			return nil, missingExtension(smtp.ExtMTRK)
		}
		if opts.Delivery == nil || opts.Delivery.DSN == nil || opts.Delivery.DSN.EnvelopeID == "" {
			return nil, errors.New("smtpclient: RFC 3885 MTRK requires a DSN ENVID")
		}
		if !validMTRKENVID(opts.Delivery.DSN.EnvelopeID) {
			return nil, errors.New("smtpclient: RFC 3885 MTRK requires ENVID in local-envid@fqhn form")
		}
		params = append(params, smtp.Param{Keyword: "MTRK", Value: l.TransitID})
	}
	if l.Submitter != "" {
		if !validSubmitter(l.Submitter) {
			return nil, errors.New("smtpclient: SUBMITTER is not an RFC 5321 mailbox")
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

func validSubmitter(mailbox string) bool {
	path, err := smtpwire.ParseReversePath("FROM:<"+mailbox+">", smtpwire.PathOptions{})
	return err == nil && path.Mailbox == mailbox && len(path.Params) == 0
}

func validMTRK(value string) bool {
	certifier, timeout, hasTimeout := strings.Cut(value, ":")
	if certifier == "" || strings.Contains(timeout, ":") {
		return false
	}
	for _, c := range certifier {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' {
			continue
		}
		return false
	}
	if !hasTimeout {
		return true
	}
	if timeout == "" || len(timeout) > 9 {
		return false
	}
	for _, c := range timeout {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func validMTRKENVID(value string) bool {
	local, fqhn, ok := strings.Cut(value, "@")
	return ok && local != "" && fqhn != "" && !strings.Contains(fqhn, "@")
}
