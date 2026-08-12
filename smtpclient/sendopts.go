package smtpclient

// MailSendOptions carries client-side policy for one MAIL FROM command (RFC
// 5321 §4.1.1.2) — how this client transmits the command, as distinct from the
// esmtp-params it transmits, which are smtp.MailOptions. A nil
// *MailSendOptions is always valid and means defaults.
//
// The split exists because smtp.MailOptions is direction-neutral vocabulary,
// shared with a server's receive-side parser, and a field that is meaningless in
// the receive direction does not belong in it (docs/API-STABILITY.md §10). This
// struct is where such client-only policy goes, now and for future extensions.
//
// Callers constructing a MailSendOptions literal must use keyed fields.
type MailSendOptions struct {
	// AllowUnadvertisedParameters permits smtp.MailOptions.Extra parameters,
	// and the RFC 4954 AUTH= parameter when set, even when the server did not
	// advertise their extension keyword. It defaults to false so the client
	// reports a local, actionable validation error before writing a command
	// that a strict server would reject. Enable it only when the caller has
	// independent knowledge that the peer accepts the parameter.
	AllowUnadvertisedParameters bool

	_ struct{}
}

// RcptSendOptions carries client-side policy for one RCPT TO command (RFC 5321
// §4.1.1.3). A nil *RcptSendOptions is always valid and means defaults. It is
// the recipient-side counterpart of MailSendOptions and exists for the same
// reason.
//
// Callers constructing a RcptSendOptions literal must use keyed fields.
type RcptSendOptions struct {
	// AllowUnadvertisedParameters permits smtp.RcptOptions.Extra parameters
	// even when the server did not advertise their extension keyword. It has
	// the same default and purpose as
	// MailSendOptions.AllowUnadvertisedParameters.
	AllowUnadvertisedParameters bool

	_ struct{}
}

// allowUnadvertised reports the opt-out for a possibly nil *MailSendOptions.
func (o *MailSendOptions) allowUnadvertised() bool {
	return o != nil && o.AllowUnadvertisedParameters
}

// allowUnadvertised reports the opt-out for a possibly nil *RcptSendOptions.
func (o *RcptSendOptions) allowUnadvertised() bool {
	return o != nil && o.AllowUnadvertisedParameters
}
