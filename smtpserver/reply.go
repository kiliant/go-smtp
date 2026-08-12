package smtpserver

import "github.com/kiliant/go-smtp"

// normalizeEnhancedCode enforces RFC 2034 section 4 without changing the
// backend's primary delivery decision. Its caller reports repaired=true through
// both tracing and the error logger before emitting the reply.
func normalizeEnhancedCode(code int, enhanced smtp.EnhancedCode) (normalized smtp.EnhancedCode, repaired bool) {
	if enhanced.String() == "" || enhanced.Class == code/100 {
		return enhanced, false
	}
	switch code / 100 {
	case 2, 4, 5:
		class := code / 100
		return smtp.EnhancedCode{Class: class, Subject: 0, Detail: 0}, true
	default:
		return enhanced, false
	}
}

func normalizeRecipientResult(result smtp.RecipientResult) (smtp.RecipientResult, bool) {
	normalized, repaired := normalizeEnhancedCode(result.Code, result.Enhanced)
	result.Enhanced = normalized
	return result, repaired
}
