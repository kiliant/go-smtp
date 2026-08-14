package smtpserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func parseExtensionMailParameter(opts *smtp.MailOptions, param smtpwire.Param, extensions map[smtp.Extension]string, seen map[string]bool) (bool, error) {
	keyword := strings.ToUpper(param.Keyword)
	var extension smtp.Extension
	switch keyword {
	case "RET", "ENVID":
		extension = smtp.ExtDSN
	case "BY":
		extension = smtp.ExtDeliverBy
	case "HOLDFOR", "HOLDUNTIL":
		extension = smtp.ExtFutureRelease
	case "MT-PRIORITY":
		extension = smtp.ExtMTPriority
	case "REQUIRETLS":
		extension = smtp.ExtRequireTLS
	case "SOLICIT":
		extension = smtp.ExtNoSoliciting
	case "MTRK":
		extension = smtp.ExtMTRK
	case "SUBMITTER":
		extension = smtp.ExtSubmitter
	case "CONPERM":
		extension = smtp.ExtConPerm
	default:
		return false, nil
	}
	params, advertised := extensionParameter(extensions, extension)
	if !advertised {
		return true, unavailableParameter(keyword)
	}
	if err := uniqueParameter(seen, keyword); err != nil {
		return true, err
	}

	switch keyword {
	case "RET":
		if !validExtensionValue(param.Value) {
			return true, invalidParameter(keyword, errors.New("value is required"))
		}
		ensureDelivery(opts).DSN = ensureDSNMail(ensureDelivery(opts).DSN)
		opts.Delivery.DSN.Return = smtp.DSNReturn(strings.ToUpper(param.Value))
	case "ENVID":
		decoded, err := decodeRequiredXtext(keyword, param.Value)
		if err != nil {
			return true, err
		}
		ensureDelivery(opts).DSN = ensureDSNMail(ensureDelivery(opts).DSN)
		opts.Delivery.DSN.EnvelopeID = decoded
		opts.Delivery.DSN.EnvelopeIDOriginal = originalParam(param)
	case "BY":
		value, err := parseDeliverBy(param.Value, params)
		if err != nil {
			return true, invalidParameter(keyword, err)
		}
		ensureDelivery(opts).DeliverBy = value
	case "HOLDFOR", "HOLDUNTIL":
		other := "HOLDFOR"
		if keyword == other {
			other = "HOLDUNTIL"
		}
		if seen[other] {
			return true, invalidParameter(keyword, errors.New("HOLDFOR and HOLDUNTIL are mutually exclusive"))
		}
		value, err := parseFutureRelease(keyword, param.Value, params)
		if err != nil {
			return true, invalidParameter(keyword, err)
		}
		ensureDelivery(opts).FutureRelease = value
	case "MT-PRIORITY":
		if !validSignedDecimal(param.Value) {
			return true, invalidParameter(keyword, errors.New("value must be a signed decimal integer"))
		}
		ensureDelivery(opts).MTPriority = smtp.MTPriority(param.Value)
	case "REQUIRETLS":
		if param.Value != "" {
			return true, invalidParameter(keyword, errors.New("parameter must not have a value"))
		}
		ensureDelivery(opts).RequireTLS = true
	case "SOLICIT":
		if !validSolicitValue(param.Value) {
			return true, invalidParameter(keyword, errors.New("invalid solicitation keyword list"))
		}
		ensureLegacy(opts).Solicit = param.Value
	case "MTRK":
		if !validMTRK(param.Value) {
			return true, invalidParameter(keyword, errors.New("value must be a base64 certifier with an optional nine-digit timeout"))
		}
		ensureLegacy(opts).TransitID = param.Value
	case "SUBMITTER":
		decoded, err := decodeRequiredXtext(keyword, param.Value)
		if err != nil {
			return true, err
		}
		if !validSubmitter(decoded) {
			return true, invalidParameter(keyword, errors.New("value is not an RFC 5321 mailbox"))
		}
		ensureLegacy(opts).Submitter = decoded
		opts.Legacy.SubmitterOriginal = originalParam(param)
	case "CONPERM":
		if param.Value != "" {
			return true, invalidParameter(keyword, errors.New("parameter must not have a value"))
		}
		ensureLegacy(opts).ConPerm = true
	}
	return true, nil
}

func parseExtensionRcptParameter(opts *smtp.RcptOptions, param smtpwire.Param, extensions map[smtp.Extension]string, seen map[string]bool) (bool, error) {
	keyword := strings.ToUpper(param.Keyword)
	var extension smtp.Extension
	switch keyword {
	case "NOTIFY", "ORCPT":
		extension = smtp.ExtDSN
	case "RRVS":
		extension = smtp.ExtRRVS
	case "CONNEG":
		extension = smtp.ExtConNeg
	default:
		return false, nil
	}
	if _, advertised := extensionParameter(extensions, extension); !advertised {
		return true, unavailableParameter(keyword)
	}
	if err := uniqueParameter(seen, keyword); err != nil {
		return true, err
	}

	switch keyword {
	case "NOTIFY":
		values, err := parseNotify(param.Value)
		if err != nil {
			return true, invalidParameter(keyword, err)
		}
		ensureRecipientDelivery(opts).DSN = ensureDSNRcpt(ensureRecipientDelivery(opts).DSN)
		opts.Delivery.DSN.Notify = values
	case "ORCPT":
		addressType, encoded, ok := strings.Cut(param.Value, ";")
		if !ok || !validAtom(addressType) || encoded == "" {
			return true, invalidParameter(keyword, errors.New("value must be addr-type;xtext-address"))
		}
		decoded, err := smtpwire.DecodeXtext(encoded)
		if err != nil {
			return true, invalidParameter(keyword, err)
		}
		ensureRecipientDelivery(opts).DSN = ensureDSNRcpt(ensureRecipientDelivery(opts).DSN)
		opts.Delivery.DSN.OriginalType = addressType
		opts.Delivery.DSN.Original = decoded
		opts.Delivery.DSN.ORCPTOriginal = originalParam(param)
	case "RRVS":
		value, err := parseRRVS(param.Value)
		if err != nil {
			return true, invalidParameter(keyword, err)
		}
		ensureRecipientDelivery(opts).RRVS = value
	case "CONNEG":
		if param.Value != "" {
			return true, invalidParameter(keyword, errors.New("parameter must not have a value"))
		}
		ensureRecipientLegacy(opts).ConNeg = true
	}
	return true, nil
}

func validateExtensionMailPath(reversePath string, opts *smtp.MailOptions) error {
	if reversePath == "" && opts != nil && opts.Delivery != nil && opts.Delivery.FutureRelease != nil {
		return invalidParameter("FUTURERELEASE", errors.New("must not be used with the null reverse-path"))
	}
	if opts != nil && opts.Legacy != nil && opts.Legacy.TransitID != "" {
		if opts.Delivery == nil || opts.Delivery.DSN == nil || opts.Delivery.DSN.EnvelopeIDOriginal == nil {
			return invalidParameter("MTRK", errors.New("requires ENVID"))
		}
		if !validMTRKENVID(opts.Delivery.DSN.EnvelopeIDOriginal.Value) {
			return invalidParameter("MTRK", errors.New("requires ENVID in local-envid@fqhn form"))
		}
	}
	return nil
}

func parseDeliverBy(value, capability string) (*smtp.DeliverByOptions, error) {
	parts := strings.Split(value, ";")
	if len(parts) != 2 || parts[0] == "" {
		return nil, errors.New("value must be by-time;by-mode[T]")
	}
	digits := strings.TrimLeft(parts[0], "+-")
	if len(digits) > 9 || !validSignedDecimal(parts[0]) {
		return nil, errors.New("by-time must contain at most nine decimal digits")
	}
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || seconds < -999999999 || seconds > 999999999 {
		return nil, errors.New("by-time must be in -999999999..999999999")
	}
	modeAndTrace := strings.ToUpper(parts[1])
	trace := strings.HasSuffix(modeAndTrace, "T")
	mode := strings.TrimSuffix(modeAndTrace, "T")
	if mode != "N" && mode != "R" {
		return nil, errors.New("by-mode must be N or R")
	}
	if mode == "R" && seconds < 1 {
		return nil, errors.New("return mode requires a positive by-time")
	}
	if mode == "R" && capability != "" {
		minimumText, _, _ := strings.Cut(capability, ",")
		if minimumText == "" {
			return &smtp.DeliverByOptions{Seconds: seconds, Mode: mode, Trace: trace}, nil
		}
		minimum, err := strconv.ParseInt(minimumText, 10, 64)
		if err != nil || seconds < minimum {
			return nil, fmt.Errorf("return deadline must be at least %s seconds", minimumText)
		}
	}
	return &smtp.DeliverByOptions{Seconds: seconds, Mode: mode, Trace: trace}, nil
}

func parseFutureRelease(keyword, value, capability string) (*smtp.FutureReleaseOptions, error) {
	fields := strings.Fields(capability)
	if len(fields) != 2 {
		return nil, errors.New("server FUTURERELEASE declaration is invalid")
	}
	maximum, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil, errors.New("server FUTURERELEASE interval is invalid")
	}
	maximumTime, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return nil, errors.New("server FUTURERELEASE date-time is invalid")
	}
	result := &smtp.FutureReleaseOptions{}
	if keyword == "HOLDFOR" {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds < 1 || seconds > maximum || strconv.FormatInt(seconds, 10) != value {
			return nil, fmt.Errorf("value must be in 1..%d", maximum)
		}
		result.HoldForSeconds = seconds
		return result, nil
	}
	timestamp, err := time.Parse(time.RFC3339, value)
	if err != nil || timestamp.After(maximumTime) {
		return nil, errors.New("value is not a valid date-time within the advertised maximum")
	}
	result.HoldUntil = timestamp.UTC().Format(time.RFC3339)
	return result, nil
}

func parseNotify(value string) ([]smtp.DSNNotify, error) {
	if value == "" {
		return nil, errors.New("value is required")
	}
	seen := make(map[string]bool)
	parts := strings.Split(value, ",")
	result := make([]smtp.DSNNotify, 0, len(parts))
	for _, part := range parts {
		part = strings.ToUpper(part)
		if !validAtom(part) {
			return nil, errors.New("contains an invalid notification token")
		}
		if seen[part] {
			return nil, errors.New("contains a duplicate notification token")
		}
		seen[part] = true
		result = append(result, smtp.DSNNotify(part))
	}
	if seen["NEVER"] && len(result) != 1 {
		return nil, errors.New("NEVER must be used alone")
	}
	return result, nil
}

func parseRRVS(value string) (*smtp.RRVSOptions, error) {
	parts := strings.Split(value, ";")
	if len(parts) > 2 || parts[0] == "" {
		return nil, errors.New("value must be date-time[;C|R]")
	}
	timestamp, err := time.Parse(time.RFC3339, parts[0])
	if err != nil || len(parts[0]) > len("2006-01-02T15:04:05Z") && parts[0][len("2006-01-02T15:04:05")] == '.' {
		return nil, errors.New("timestamp is invalid")
	}
	disposition := ""
	if len(parts) == 2 {
		disposition = strings.ToUpper(parts[1])
		if disposition != "C" && disposition != "R" {
			return nil, errors.New("disposition must be C or R")
		}
	}
	return &smtp.RRVSOptions{Timestamp: timestamp.UTC().Format(time.RFC3339), Disposition: disposition}, nil
}

func decodeRequiredXtext(keyword, value string) (string, error) {
	if value == "" {
		return "", invalidParameter(keyword, errors.New("xtext value is required"))
	}
	decoded, err := smtpwire.DecodeXtext(value)
	if err != nil {
		return "", invalidParameter(keyword, err)
	}
	return decoded, nil
}

func originalParam(param smtpwire.Param) *smtp.Param {
	return &smtp.Param{Keyword: param.Keyword, Value: param.Value}
}

func validSignedDecimal(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '-' || value[0] == '+' {
		value = value[1:]
		if value == "" {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validExtensionValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, " \t\r\n\x00=")
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

func validAtom(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", r) {
			continue
		}
		return false
	}
	return true
}

func validSolicitValue(value string) bool {
	if value == "" || len(value) > 1000 || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, keyword := range strings.Split(value, ",") {
		if keyword == "" {
			return false
		}
		for _, r := range keyword {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`{|}~:", r) {
				continue
			}
			return false
		}
	}
	return true
}

func ensureDelivery(opts *smtp.MailOptions) *smtp.DeliveryOptions {
	if opts.Delivery == nil {
		opts.Delivery = &smtp.DeliveryOptions{}
	}
	return opts.Delivery
}

func ensureDSNMail(opts *smtp.DSNMailOptions) *smtp.DSNMailOptions {
	if opts == nil {
		return &smtp.DSNMailOptions{}
	}
	return opts
}

func ensureLegacy(opts *smtp.MailOptions) *smtp.LegacyOptions {
	if opts.Legacy == nil {
		opts.Legacy = &smtp.LegacyOptions{}
	}
	return opts.Legacy
}

func ensureRecipientDelivery(opts *smtp.RcptOptions) *smtp.RecipientDeliveryOptions {
	if opts.Delivery == nil {
		opts.Delivery = &smtp.RecipientDeliveryOptions{}
	}
	return opts.Delivery
}

func ensureDSNRcpt(opts *smtp.DSNRcptOptions) *smtp.DSNRcptOptions {
	if opts == nil {
		return &smtp.DSNRcptOptions{}
	}
	return opts
}

func ensureRecipientLegacy(opts *smtp.RcptOptions) *smtp.RecipientLegacyOptions {
	if opts.Legacy == nil {
		opts.Legacy = &smtp.RecipientLegacyOptions{}
	}
	return opts.Legacy
}
