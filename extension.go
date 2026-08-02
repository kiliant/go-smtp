package smtp

// Extension is an EHLO extension keyword, as sent by the server on its own
// line of the EHLO reply (RFC 5321 §4.1.1.1, "ehlo-keyword"). It is a
// string-backed named type rather than a closed enum, per
// docs/API-STABILITY.md §1a: the IANA SMTP Service Extensions registry has
// grown from RFC 821's handful to LIMITS (RFC 9422) and will grow again, and
// a keyword this library has never heard of must still reach the caller —
// together with its parameters, which is where SIZE, AUTH and LIMITS carry
// their payload — rather than being silently dropped.
//
// Named constants exist below for every keyword in docs/RFC-COVERAGE.md.
// Constructing an Extension from a keyword this library does not (yet) name
// is valid and expected: Extension("SOME-FUTURE-EXTENSION") is exactly how
// an unrecognised keyword is preserved and compared.
type Extension string

// Base RFC 5321 session extensions.
const (
	// ExtStartTLS is STARTTLS (RFC 3207).
	ExtStartTLS Extension = "STARTTLS"
	// ExtPipelining is PIPELINING (RFC 2920).
	ExtPipelining Extension = "PIPELINING"
	// ExtEnhancedStatusCodes is ENHANCEDSTATUSCODES (RFC 2034).
	ExtEnhancedStatusCodes Extension = "ENHANCEDSTATUSCODES"
	// ExtAuth is AUTH (RFC 4954).
	ExtAuth Extension = "AUTH"
)

// Group A — transport core (docs/RFC-COVERAGE.md, task T08).
const (
	// ExtSize is SIZE (RFC 1870).
	ExtSize Extension = "SIZE"
	// Ext8BitMIME is 8BITMIME (RFC 6152; widely miscited as RFC 1652, the
	// RFC 6152 obsoletes).
	Ext8BitMIME Extension = "8BITMIME"
	// ExtSMTPUTF8 is SMTPUTF8 (RFC 6531).
	ExtSMTPUTF8 Extension = "SMTPUTF8"
	// ExtChunking is CHUNKING, the BDAT command (RFC 3030).
	ExtChunking Extension = "CHUNKING"
	// ExtBinaryMIME is BINARYMIME (RFC 3030).
	ExtBinaryMIME Extension = "BINARYMIME"
	// ExtUTF8SMTP is UTF8SMTP (RFC 5336), the obsoleted predecessor of
	// SMTPUTF8 (RFC 6531). Recognised on the wire for compatibility with
	// servers that still advertise it; this library never sends it as a
	// preference and does not implement its distinct address syntax.
	ExtUTF8SMTP Extension = "UTF8SMTP"
)

// Group B — delivery control (docs/RFC-COVERAGE.md, task T09).
const (
	// ExtDSN is DSN, delivery status notifications (RFC 3461).
	ExtDSN Extension = "DSN"
	// ExtDeliverBy is DELIVERBY (RFC 2852).
	ExtDeliverBy Extension = "DELIVERBY"
	// ExtFutureRelease is FUTURERELEASE, the HOLDFOR=/HOLDUNTIL= parameters
	// (RFC 4865). A registry scrape during this repository's setup
	// misattributed these to RFC 6729, which defines no SMTP keyword; RFC
	// 4865 is the confirmed citation.
	ExtFutureRelease Extension = "FUTURERELEASE"
	// ExtMTPriority is MT-PRIORITY (RFC 6710).
	ExtMTPriority Extension = "MT-PRIORITY"
	// ExtRRVS is RRVS, Require-Recipient-Valid-Since (RFC 7293).
	ExtRRVS Extension = "RRVS"
	// ExtRequireTLS is REQUIRETLS (RFC 8689).
	ExtRequireTLS Extension = "REQUIRETLS"
	// ExtLimits is LIMITS (RFC 9422).
	ExtLimits Extension = "LIMITS"
	// ExtBURL is BURL (RFC 4468).
	ExtBURL Extension = "BURL"
)

// Group C — legacy & niche (docs/RFC-COVERAGE.md, task T10). A "deferred"
// keyword in that table still parses and is preserved by this type; only the
// command support is best-effort or absent.
const (
	// ExtETRN is ETRN, a queue-start command (RFC 1985).
	ExtETRN Extension = "ETRN"
	// ExtATRN is ATRN, authenticated TURN / ODMR (RFC 2645).
	ExtATRN Extension = "ATRN"
	// ExtNoSoliciting is NO-SOLICITING, the SOLICIT= parameter (RFC 3865).
	ExtNoSoliciting Extension = "NO-SOLICITING"
	// ExtMTRK is MTRK, the TRANSID= parameter (RFC 3885).
	ExtMTRK Extension = "MTRK"
	// ExtSubmitter is SUBMITTER, the SUBMITTER= parameter (RFC 4405).
	ExtSubmitter Extension = "SUBMITTER"
	// ExtConPerm is CONPERM, content conversion permission (RFC 4141).
	ExtConPerm Extension = "CONPERM"
	// ExtConNeg is CONNEG, content negotiation (RFC 4141).
	ExtConNeg Extension = "CONNEG"
	// ExtCheckpoint is CHECKPOINT (RFC 1845). Deferred: no known server
	// support.
	ExtCheckpoint Extension = "CHECKPOINT"
	// ExtVerb is VERB, sendmail's verbose mode. Not defined by any RFC
	// (Eric Allman / sendmail extension). Deferred.
	ExtVerb Extension = "VERB"
	// ExtOnex is ONEX, one-transaction-only. Not defined by any RFC (Eric
	// Allman / sendmail extension). Deferred.
	ExtOnex Extension = "ONEX"
	// ExtSend is SEND (RFC 821), removed by RFC 5321. Parsed if
	// advertised; never sent.
	ExtSend Extension = "SEND"
	// ExtSoml is SOML (RFC 821), removed by RFC 5321. Parsed if
	// advertised; never sent.
	ExtSoml Extension = "SOML"
	// ExtSaml is SAML (RFC 821), removed by RFC 5321. Parsed if
	// advertised; never sent.
	ExtSaml Extension = "SAML"
	// ExtTurn is TURN (RFC 821), removed by RFC 5321 for security reasons
	// and superseded by ETRN/ATRN. Parsed if advertised; never sent.
	ExtTurn Extension = "TURN"
)
