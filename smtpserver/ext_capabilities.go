package smtpserver

import (
	"strconv"
	"strings"
	"time"

	"github.com/kiliant/go-smtp"
)

const backendATRN backendFeatureSet = 1 << 3

var frameworkCapabilities = map[smtp.Extension]bool{
	smtp.ExtPipelining:          true,
	smtp.ExtSize:                true,
	smtp.Ext8BitMIME:            true,
	smtp.ExtEnhancedStatusCodes: true,
	smtp.ExtStartTLS:            true,
	smtp.ExtAuth:                true,
	smtp.ExtSMTPUTF8:            true,
	smtp.ExtChunking:            true,
	smtp.ExtBinaryMIME:          true,
	smtp.ExtLimits:              true,
	smtp.ExtATRN:                true,
	smtp.ExtETRN:                true,
	smtp.ExtBURL:                true,
}

func validateExtensionSession(session *Session) []string {
	var problems []string
	seen := make(map[smtp.Extension]bool)
	for i, capability := range session.ParameterExtensions {
		keyword := capability.Keyword
		label := "Session.ParameterExtensions[" + strconv.Itoa(i) + "]"
		if !validExtensionKeyword(string(keyword)) {
			problems = append(problems, label+" has an invalid upper-case Keyword")
			continue
		}
		if frameworkCapabilities[keyword] {
			problems = append(problems, label+" duplicates framework-owned capability "+string(keyword))
			continue
		}
		if seen[keyword] {
			problems = append(problems, label+" duplicates "+string(keyword))
			continue
		}
		seen[keyword] = true
		if !validCapabilityParams(capability.Params) {
			problems = append(problems, label+" Params contain invalid EHLO framing")
			continue
		}
		if problem := validateKnownCapabilityParams(keyword, capability.Params); problem != "" {
			problems = append(problems, label+" "+problem)
		}
	}
	if seen[smtp.ExtMTRK] && !seen[smtp.ExtDSN] {
		problems = append(problems, "Session.ParameterExtensions MTRK requires DSN")
	}
	if session.Limits != nil {
		for name, value := range map[string]uint32{
			"MAILMAX":       session.Limits.MailMax,
			"RCPTMAX":       session.Limits.RcptMax,
			"RCPTDOMAINMAX": session.Limits.RcptDomainMax,
		} {
			if value > 999999 {
				problems = append(problems, "Session.Limits."+name+" exceeds RFC 9422's six-digit maximum")
			}
		}
		limitNames := map[string]bool{"MAILMAX": true, "RCPTMAX": true, "RCPTDOMAINMAX": true}
		extraFields := strings.Fields(session.Limits.Extra)
		if strings.Join(extraFields, " ") != session.Limits.Extra {
			problems = append(problems, "Session.Limits.Extra has invalid spacing or framing")
		}
		for i, field := range extraFields {
			label := "Session.Limits.Extra field " + strconv.Itoa(i)
			keyword, value, hasValue := strings.Cut(field, "=")
			if !validLimitName(keyword) {
				problems = append(problems, label+" has an invalid limit name")
				continue
			}
			canonical := strings.ToUpper(keyword)
			if limitNames[canonical] {
				problems = append(problems, label+" duplicates "+canonical)
				continue
			}
			limitNames[canonical] = true
			if hasValue && !validLimitValue(value) {
				problems = append(problems, label+" has an invalid RFC 9422 limit value")
			}
		}
	}
	if session.ATRN != nil && session.Authenticate == nil && session.ChallengeResponse == nil && session.SCRAMCredentials == nil {
		problems = append(problems, "Session.ATRN requires an authentication verifier")
	}
	return problems
}

func validExtensionKeyword(keyword string) bool {
	if keyword == "" || keyword != strings.ToUpper(keyword) {
		return false
	}
	for i := 0; i < len(keyword); i++ {
		c := keyword[i]
		if c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			continue
		}
		return false
	}
	return keyword[0] >= 'A' && keyword[0] <= 'Z'
}

func validCapabilityParams(params string) bool {
	if params == "" {
		return true
	}
	if strings.TrimSpace(params) != params {
		return false
	}
	for i := 0; i < len(params); i++ {
		if params[i] < 0x20 || params[i] > 0x7e {
			return false
		}
	}
	return true
}

func validLimitName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validLimitValue(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 0x21 && c <= 0x3a || c >= 0x3c && c <= 0x7e {
			continue
		}
		return false
	}
	return true
}

func validateKnownCapabilityParams(keyword smtp.Extension, params string) string {
	switch keyword {
	case smtp.ExtDeliverBy:
		parts := strings.Split(params, ",")
		if parts[0] != "" {
			minimum, err := strconv.ParseInt(parts[0], 10, 64)
			if err != nil || minimum < 1 || minimum > 999999999 || strconv.FormatInt(minimum, 10) != parts[0] {
				return "has an invalid DELIVERBY minimum"
			}
		}
		for _, token := range parts[1:] {
			if token == "" || strings.ContainsAny(token, " ,\t\r\n\x00") {
				return "has an invalid DELIVERBY extension token"
			}
		}
	case smtp.ExtFutureRelease:
		fields := strings.Fields(params)
		if len(fields) != 2 {
			return "must declare FUTURERELEASE maximum interval and date-time"
		}
		maximum, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || maximum < 1 || maximum > 999999999 {
			return "has an invalid FUTURERELEASE maximum interval"
		}
		if _, err := time.Parse(time.RFC3339, fields[1]); err != nil {
			return "has an invalid FUTURERELEASE maximum date-time"
		}
	case smtp.ExtNoSoliciting:
		if params != "" && !validSolicitValue(params) {
			return "has invalid NO-SOLICITING keywords"
		}
	}
	return ""
}

func extensionBackendFeatures(session *Session) backendFeatureSet {
	if session != nil && session.ATRN != nil {
		return backendATRN
	}
	return 0
}

func (s *commandSession) parameterExtensionMap() map[smtp.Extension]string {
	if len(s.backend.ParameterExtensions) == 0 {
		return nil
	}
	result := make(map[smtp.Extension]string, len(s.backend.ParameterExtensions))
	for _, capability := range s.backend.ParameterExtensions {
		if capability.Keyword == smtp.ExtFutureRelease && s.server.mode == modeLMTP {
			continue
		}
		result[capability.Keyword] = capability.Params
	}
	return result
}

func (s *commandSession) extensionCapabilityDescriptors() []capabilityDescriptor {
	descriptors := make([]capabilityDescriptor, 0, len(s.backend.ParameterExtensions)+2)
	for _, declared := range s.backend.ParameterExtensions {
		declared := declared
		modes := modeSetBoth
		if declared.Keyword == smtp.ExtFutureRelease {
			modes = modeSetSMTP
		}
		descriptors = append(descriptors, capabilityDescriptor{
			keyword: declared.Keyword,
			params: func(capabilityContext) string {
				return declared.Params
			},
			modes: modes,
		})
	}
	if s.backend.Limits != nil {
		descriptors = append(descriptors, capabilityDescriptor{
			keyword: smtp.ExtLimits,
			params: func(capabilityContext) string {
				return formatLimits(*s.backend.Limits)
			},
			modes: modeSetBoth,
		})
	}
	descriptors = append(descriptors, capabilityDescriptor{
		keyword:         smtp.ExtATRN,
		requiresBackend: backendATRN,
		modes:           modeSetSMTP,
		available: func(capabilityContext) bool {
			return len(s.authMechanisms()) != 0
		},
	})
	return descriptors
}

func formatLimits(limits smtp.Limits) string {
	fields := make([]string, 0, 4)
	if limits.MailMax != 0 {
		fields = append(fields, "MAILMAX="+strconv.FormatUint(uint64(limits.MailMax), 10))
	}
	if limits.RcptMax != 0 {
		fields = append(fields, "RCPTMAX="+strconv.FormatUint(uint64(limits.RcptMax), 10))
	}
	if limits.RcptDomainMax != 0 {
		fields = append(fields, "RCPTDOMAINMAX="+strconv.FormatUint(uint64(limits.RcptDomainMax), 10))
	}
	if limits.Extra != "" {
		fields = append(fields, limits.Extra)
	}
	return strings.Join(fields, " ")
}

func extensionParameter(extension map[smtp.Extension]string, keyword smtp.Extension) (string, bool) {
	value, ok := extension[keyword]
	return value, ok
}
