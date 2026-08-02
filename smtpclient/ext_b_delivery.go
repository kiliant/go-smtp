package smtpclient

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

func init() {
	registerMailExtension("delivery-control", (*Client).deliveryMailParams)
	registerRcptExtension("dsn", (*Client).dsnRcptParams)
}

func (c *Client) deliveryMailParams(path string, opts *smtp.MailOptions) ([]smtp.Param, error) {
	if opts == nil || opts.Delivery == nil {
		return nil, nil
	}
	d := opts.Delivery
	var params []smtp.Param
	if d.DSN != nil {
		p, err := dsnMailParams(d.DSN)
		if err != nil {
			return nil, err
		}
		params = append(params, p...)
	}
	if d.DeliverBy != nil {
		p, err := c.deliverByParam(d.DeliverBy)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	if d.FutureRelease != nil {
		if path == "<>" {
			return nil, errors.New("smtpclient: FUTURERELEASE must not be requested for a delivery status notification")
		}
		p, err := c.futureReleaseParam(d.FutureRelease)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	if d.MTPriority != "" {
		p, err := mtPriorityParam(d.MTPriority)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	if d.RRVS != nil {
		return nil, errors.New("smtpclient: RRVS is a RCPT TO parameter; smtp.RecipientDeliveryOptions needs an RRVS field")
	}
	if d.RequireTLS {
		params = append(params, smtp.Param{Keyword: "REQUIRETLS"})
	}
	return params, nil
}

func dsnMailParams(d *smtp.DSNMailOptions) ([]smtp.Param, error) {
	var params []smtp.Param
	if d.Return != "" {
		v := strings.ToUpper(string(d.Return))
		if v != string(smtp.DSNReturnFull) && v != string(smtp.DSNReturnHeaders) {
			return nil, fmt.Errorf("smtpclient: invalid DSN RET value %q", d.Return)
		}
		params = append(params, smtp.Param{Keyword: "RET", Value: v})
	}
	if d.EnvelopeID != "" {
		params = append(params, smtp.Param{Keyword: "ENVID", Value: smtp.EncodeXtext(d.EnvelopeID)})
	}
	return params, nil
}

func (c *Client) dsnRcptParams(_ string, opts *smtp.RcptOptions) ([]smtp.Param, error) {
	if opts == nil || opts.Delivery == nil {
		return nil, nil
	}
	var params []smtp.Param
	if d := opts.Delivery.DSN; d != nil {
		if len(d.Notify) != 0 {
			v, err := dsnNotifyValue(d.Notify)
			if err != nil {
				return nil, err
			}
			params = append(params, smtp.Param{Keyword: "NOTIFY", Value: v})
		}
		if d.OriginalType != "" || d.Original != "" {
			if d.OriginalType == "" || d.Original == "" {
				return nil, errors.New("smtpclient: ORCPT requires both original address type and address")
			}
			if !isAtom(d.OriginalType) {
				return nil, fmt.Errorf("smtpclient: invalid ORCPT address type %q", d.OriginalType)
			}
			params = append(params, smtp.Param{Keyword: "ORCPT", Value: d.OriginalType + ";" + smtp.EncodeXtext(d.Original)})
		}
	}
	if opts.Delivery.RRVS != nil {
		p, err := rrvsParam(opts.Delivery.RRVS)
		if err != nil {
			return nil, err
		}
		params = append(params, p)
	}
	return params, nil
}

func dsnNotifyValue(notify []smtp.DSNNotify) (string, error) {
	seen := make(map[string]bool)
	var values []string
	for _, part := range notify {
		for _, v := range strings.Split(string(part), ",") {
			v = strings.ToUpper(v)
			switch v {
			case "SUCCESS", "FAILURE", "DELAY", "NEVER":
			default:
				return "", fmt.Errorf("smtpclient: invalid DSN NOTIFY value %q", v)
			}
			if seen[v] {
				return "", fmt.Errorf("smtpclient: duplicate DSN NOTIFY value %q", v)
			}
			seen[v] = true
			values = append(values, v)
		}
	}
	if seen["NEVER"] && len(values) != 1 {
		return "", errors.New("smtpclient: DSN NOTIFY=NEVER must be used alone")
	}
	return strings.Join(values, ","), nil
}

func (c *Client) deliverByParam(d *smtp.DeliverByOptions) (smtp.Param, error) {
	if d.Seconds < 1 || d.Seconds > 999999999 {
		return smtp.Param{}, errors.New("smtpclient: DELIVERBY seconds must be in 1..999999999")
	}
	mode := strings.ToUpper(d.Mode)
	if mode != "" && mode != "N" && mode != "R" {
		return smtp.Param{}, fmt.Errorf("smtpclient: invalid DELIVERBY mode %q", d.Mode)
	}
	v := strconv.FormatInt(d.Seconds, 10)
	if mode != "" {
		v += ";" + mode
	}
	if mode == "R" {
		if params, ok := c.Extension(smtp.ExtDeliverBy); ok && params != "" {
			minimum, err := strconv.ParseInt(params, 10, 64)
			if err != nil || minimum < 1 {
				return smtp.Param{}, errors.New("smtpclient: invalid DELIVERBY EHLO parameter")
			}
			if d.Seconds < minimum {
				return smtp.Param{}, fmt.Errorf("smtpclient: DELIVERBY return deadline must be at least %d seconds", minimum)
			}
		}
	}
	return smtp.Param{Keyword: "BY", Value: v}, nil
}

func (c *Client) futureReleaseParam(f *smtp.FutureReleaseOptions) (smtp.Param, error) {
	if (f.HoldForSeconds == 0) == (f.HoldUntil == "") {
		return smtp.Param{}, errors.New("smtpclient: exactly one of HOLDFOR and HOLDUNTIL is required")
	}
	max, ok := c.Extension(smtp.ExtFutureRelease)
	if !ok {
		return smtp.Param{}, errors.New("smtpclient: server did not advertise extension \"FUTURERELEASE\"")
	}
	fields := strings.Fields(max)
	if len(fields) != 2 {
		return smtp.Param{}, errors.New("smtpclient: invalid FUTURERELEASE EHLO parameters")
	}
	maxSeconds, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || maxSeconds < 1 {
		return smtp.Param{}, errors.New("smtpclient: invalid FUTURERELEASE maximum interval")
	}
	maxUntil, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return smtp.Param{}, errors.New("smtpclient: invalid FUTURERELEASE maximum date-time")
	}
	if f.HoldForSeconds != 0 {
		if f.HoldForSeconds < 1 || f.HoldForSeconds > maxSeconds {
			return smtp.Param{}, fmt.Errorf("smtpclient: HOLDFOR must be in 1..%d", maxSeconds)
		}
		return smtp.Param{Keyword: "HOLDFOR", Value: strconv.FormatInt(f.HoldForSeconds, 10)}, nil
	}
	t, err := time.Parse(time.RFC3339, f.HoldUntil)
	if err != nil || t.After(maxUntil) {
		return smtp.Param{}, errors.New("smtpclient: HOLDUNTIL exceeds or is not a valid FUTURERELEASE date-time")
	}
	return smtp.Param{Keyword: "HOLDUNTIL", Value: t.UTC().Format(time.RFC3339)}, nil
}

func mtPriorityParam(priority smtp.MTPriority) (smtp.Param, error) {
	v := string(priority)
	n, err := strconv.Atoi(v)
	if err != nil || n < -9 || n > 9 || (n == 0 && v != "0") || (n != 0 && strconv.Itoa(n) != v) {
		return smtp.Param{}, fmt.Errorf("smtpclient: invalid MT-PRIORITY value %q", priority)
	}
	return smtp.Param{Keyword: "MT-PRIORITY", Value: v}, nil
}

func rrvsParam(r *smtp.RRVSOptions) (smtp.Param, error) {
	if r.Timestamp == "" {
		return smtp.Param{}, errors.New("smtpclient: RRVS timestamp is required")
	}
	t, err := time.Parse(time.RFC3339, r.Timestamp)
	if err != nil {
		return smtp.Param{}, fmt.Errorf("smtpclient: invalid RRVS timestamp %q", r.Timestamp)
	}
	d := strings.ToUpper(r.Disposition)
	if d != "" && d != "C" && d != "R" {
		return smtp.Param{}, fmt.Errorf("smtpclient: invalid RRVS disposition %q", r.Disposition)
	}
	v := t.UTC().Format(time.RFC3339)
	if d != "" {
		v += ";" + d
	}
	return smtp.Param{Keyword: "RRVS", Value: v}, nil
}

func isAtom(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r) {
			continue
		}
		return false
	}
	return true
}
