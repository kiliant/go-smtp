package smtpwire

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

const defaultMaxPathLength = 4096

var (
	ErrPathPrefix       = errors.New("smtpwire: invalid path command prefix")
	ErrPathSyntax       = errors.New("smtpwire: invalid SMTP path syntax")
	ErrPathTooLong      = errors.New("smtpwire: path exceeds configured limit")
	ErrPathUTF8Required = errors.New("smtpwire: UTF-8 path requires SMTPUTF8")
	ErrNullForwardPath  = errors.New("smtpwire: null path is not valid for RCPT TO")
	ErrPathParameter    = errors.New("smtpwire: invalid trailing esmtp-param")
)

// PathOptions configures reverse-path and forward-path parsing. A zero maximum
// uses a safe default above RFC 5321's 256-octet minimum acceptance size.
type PathOptions struct {
	SMTPUTF8      bool
	MaxPathLength int
}

// Path is one parsed reverse-path or forward-path. Mailbox preserves the final
// mailbox spelling without angle brackets and without an obsolete source
// route. LocalPart and Domain preserve their wire spelling; quoted local parts
// remain quoted. Null is true only for the legal MAIL FROM:<> form.
type Path struct {
	Mailbox    string
	LocalPart  string
	Domain     string
	Null       bool
	Postmaster bool
	Params     []Param
}

// ParseReversePath parses a MAIL argument beginning with FROM:. An optional SP
// between the colon and '<' is accepted for interoperability.
func ParseReversePath(argument string, opts PathOptions) (Path, error) {
	return parsePathArgument(argument, "FROM:", true, false, opts)
}

// ParseForwardPath parses a RCPT argument beginning with TO:. It accepts the
// special Postmaster mailbox with or without a domain.
func ParseForwardPath(argument string, opts PathOptions) (Path, error) {
	return parsePathArgument(argument, "TO:", false, true, opts)
}

func parsePathArgument(argument, prefix string, allowNull, allowPostmaster bool, opts PathOptions) (Path, error) {
	if len(argument) < len(prefix) || !strings.EqualFold(argument[:len(prefix)], prefix) {
		return Path{}, fmt.Errorf("%w: want %s", ErrPathPrefix, prefix)
	}
	rest := argument[len(prefix):]
	rest = strings.TrimLeft(rest, " ")
	if rest == "" || rest[0] != '<' {
		return Path{}, ErrPathSyntax
	}
	close, err := pathClosingAngle(rest)
	if err != nil {
		return Path{}, err
	}
	maxPath := opts.MaxPathLength
	if maxPath <= 0 {
		maxPath = defaultMaxPathLength
	}
	if close+1 > maxPath {
		return Path{}, fmt.Errorf("%w: %d > %d", ErrPathTooLong, close+1, maxPath)
	}

	params, err := parsePathParams(rest[close+1:])
	if err != nil {
		return Path{}, err
	}
	rawMailbox := rest[1:close]
	if rawMailbox == "" {
		if !allowNull {
			return Path{}, ErrNullForwardPath
		}
		return Path{Null: true, Params: params}, nil
	}
	if !utf8.ValidString(rawMailbox) {
		return Path{}, ErrPathSyntax
	}
	if !opts.SMTPUTF8 && hasHighByte(rawMailbox) {
		return Path{}, ErrPathUTF8Required
	}

	mailbox, err := discardSourceRoute(rawMailbox, opts.SMTPUTF8)
	if err != nil {
		return Path{}, err
	}
	local, domain, hasDomain, err := splitMailbox(mailbox)
	if err != nil {
		return Path{}, err
	}
	if allowPostmaster && strings.EqualFold(local, "Postmaster") && (!hasDomain || domain != "") {
		if hasDomain {
			if err := validateDomain(domain, opts.SMTPUTF8); err != nil {
				return Path{}, err
			}
		}
		return Path{Mailbox: mailbox, LocalPart: local, Domain: domain, Postmaster: true, Params: params}, nil
	}
	if !hasDomain || domain == "" {
		return Path{}, ErrPathSyntax
	}
	if err := validateLocalPart(local, opts.SMTPUTF8); err != nil {
		return Path{}, err
	}
	if err := validateDomain(domain, opts.SMTPUTF8); err != nil {
		return Path{}, err
	}
	return Path{Mailbox: mailbox, LocalPart: local, Domain: domain, Params: params}, nil
}

func pathClosingAngle(rest string) (int, error) {
	quoted, escaped, literal := false, false, false
	for i := 1; i < len(rest); i++ {
		c := rest[i]
		if escaped {
			escaped = false
			continue
		}
		if quoted {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '[':
			literal = true
		case ']':
			literal = false
		case '>':
			if !literal {
				return i, nil
			}
		}
	}
	return 0, ErrPathSyntax
}

func parsePathParams(suffix string) ([]Param, error) {
	if suffix == "" {
		return nil, nil
	}
	if suffix[0] != ' ' || strings.ContainsAny(suffix, "\r\n\x00\t") {
		return nil, ErrPathParameter
	}
	fields := strings.Fields(suffix)
	if len(fields) == 0 {
		return nil, nil
	}
	params := make([]Param, 0, len(fields))
	for _, field := range fields {
		keyword, value, hasValue := strings.Cut(field, "=")
		if err := validateKeyword(keyword); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPathParameter, err)
		}
		if hasValue {
			if err := validateESMTPValue(value); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrPathParameter, err)
			}
		}
		params = append(params, Param{Keyword: keyword, Value: value})
	}
	return params, nil
}

func discardSourceRoute(mailbox string, smtpUTF8 bool) (string, error) {
	if mailbox[0] != '@' {
		return mailbox, nil
	}
	colon := sourceRouteColon(mailbox)
	if colon < 0 || colon+1 >= len(mailbox) {
		return "", ErrPathSyntax
	}
	for _, hop := range strings.Split(mailbox[:colon], ",") {
		if len(hop) < 2 || hop[0] != '@' {
			return "", ErrPathSyntax
		}
		if err := validateDomain(hop[1:], smtpUTF8); err != nil {
			return "", err
		}
	}
	return mailbox[colon+1:], nil
}

func sourceRouteColon(mailbox string) int {
	literal := false
	for i := 0; i < len(mailbox); i++ {
		switch mailbox[i] {
		case '[':
			literal = true
		case ']':
			literal = false
		case ':':
			if !literal {
				return i
			}
		}
	}
	return -1
}

func splitMailbox(mailbox string) (local, domain string, hasDomain bool, err error) {
	quoted, escaped := false, false
	at := -1
	for i := 0; i < len(mailbox); i++ {
		c := mailbox[i]
		if escaped {
			escaped = false
			continue
		}
		if quoted {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == '@' {
			if at >= 0 {
				return "", "", false, ErrPathSyntax
			}
			at = i
		}
	}
	if quoted || escaped {
		return "", "", false, ErrPathSyntax
	}
	if at < 0 {
		return mailbox, "", false, nil
	}
	return mailbox[:at], mailbox[at+1:], true, nil
}

func validateLocalPart(local string, smtpUTF8 bool) error {
	if local == "" {
		return ErrPathSyntax
	}
	if local[0] == '"' {
		if len(local) < 2 || local[len(local)-1] != '"' {
			return ErrPathSyntax
		}
		for i := 1; i < len(local)-1; i++ {
			c := local[i]
			if c == '\\' {
				i++
				if i >= len(local)-1 {
					return ErrPathSyntax
				}
				c = local[i]
				if c == '\r' || c == '\n' || c == 0 || c == 0x7f {
					return ErrPathSyntax
				}
				continue
			}
			if c == '"' {
				return ErrPathSyntax
			}
			if c == '\r' || c == '\n' || c == 0 || c == 0x7f || (c < 0x20 && c != '\t') {
				return ErrPathSyntax
			}
		}
		return nil
	}
	if local[len(local)-1] == '.' {
		return ErrPathSyntax
	}
	lastDot := true
	for i := 0; i < len(local); {
		c := local[i]
		if c == '.' {
			if lastDot {
				return ErrPathSyntax
			}
			lastDot = true
			i++
			continue
		}
		if c < utf8.RuneSelf {
			if !isAtext(c) {
				return ErrPathSyntax
			}
			i++
		} else {
			if !smtpUTF8 {
				return ErrPathUTF8Required
			}
			r, size := utf8.DecodeRuneInString(local[i:])
			if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) {
				return ErrPathSyntax
			}
			i += size
		}
		lastDot = false
	}
	return nil
}

func isAtext(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-/=?^_`{|}~", rune(c))
}

func validateDomain(domain string, smtpUTF8 bool) error {
	if domain == "" {
		return ErrPathSyntax
	}
	if domain[0] == '[' {
		if len(domain) < 3 || domain[len(domain)-1] != ']' {
			return ErrPathSyntax
		}
		inside := domain[1 : len(domain)-1]
		if strings.HasPrefix(strings.ToUpper(inside), "IPV6:") {
			addr, err := netip.ParseAddr(inside[len("IPv6:"):])
			if err != nil || !addr.Is6() {
				return ErrPathSyntax
			}
			return nil
		}
		if addr, err := netip.ParseAddr(inside); err == nil && addr.Is4() {
			return nil
		}
		tag, value, ok := strings.Cut(inside, ":")
		if !ok || tag == "" || value == "" || !validAddressLiteralTag(tag) || strings.ContainsAny(value, "[]\\\r\n\x00") || hasHighByte(value) {
			return ErrPathSyntax
		}
		return nil
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return ErrPathSyntax
		}
		for i := 0; i < len(label); {
			c := label[i]
			if c < utf8.RuneSelf {
				if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
					return ErrPathSyntax
				}
				i++
				continue
			}
			if !smtpUTF8 {
				return ErrPathUTF8Required
			}
			r, size := utf8.DecodeRuneInString(label[i:])
			if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) {
				return ErrPathSyntax
			}
			i += size
		}
	}
	return nil
}

func validAddressLiteralTag(tag string) bool {
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func hasHighByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}
