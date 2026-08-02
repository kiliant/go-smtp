// Package saslprep implements the RFC 4013 SASLprep profile.
package saslprep

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/kiliant/go-smtp/internal/unicodenorm"
)

var (
	ErrInvalidUTF8 = errors.New("saslprep: invalid UTF-8")
	ErrProhibited  = errors.New("saslprep: prohibited code point")
	ErrBidi        = errors.New("saslprep: invalid bidirectional string")
	ErrUnassigned  = errors.New("saslprep: unassigned Unicode 3.2 code point")
)

// Prepare applies RFC 4013's mappings and NFKC normalization. It rejects
// invalid UTF-8, prohibited code points, bidi violations, and code points that
// were unassigned by Unicode 3.2. The latter distinction matters because
// RFC 3454 intentionally freezes its repertoire.
func Prepare(input string) (string, error) {
	if !utf8.ValidString(input) {
		return "", ErrInvalidUTF8
	}
	mapped := make([]rune, 0, len(input))
	for _, r := range input {
		if r == 0x00ad || r == 0x034f || r == 0x1806 || (r >= 0x180b && r <= 0x180d) || (r >= 0x200b && r <= 0x200d) || r == 0x2060 || (r >= 0xfe00 && r <= 0xfe0f) || r == 0xfeff {
			continue
		}
		if r == 0x00a0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200b) || r == 0x202f || r == 0x205f || r == 0x3000 {
			r = ' '
		}
		mapped = append(mapped, r)
	}
	out := unicodenorm.NFKC(string(mapped))
	var hasRandAL, hasL bool
	for _, r := range out {
		if prohibited(r) {
			return "", fmt.Errorf("%w: U+%04X", ErrProhibited, r)
		}
		// Unicode 3.2 predates later scripts. A full generated table replaces
		// this conservative boundary; known post-3.2 planes must not slip in.
		if r > 0x2fa1d {
			return "", fmt.Errorf("%w: U+%04X", ErrUnassigned, r)
		}
		if isRandAL(r) {
			hasRandAL = true
		}
		if unicode.IsLetter(r) && !isRandAL(r) {
			hasL = true
		}
	}
	if hasRandAL && (hasL || !isRandAL(firstRune(out)) || !isRandAL(lastRune(out))) {
		return "", ErrBidi
	}
	return out, nil
}
func prohibited(r rune) bool {
	return unicode.IsControl(r) || (r >= 0xe000 && r <= 0xf8ff) ||
		(r >= 0xf0000 && r <= 0xffffd) || (r >= 0x100000 && r <= 0x10fffd) ||
		(r >= 0xd800 && r <= 0xdfff) || unicode.Is(unicode.Noncharacter_Code_Point, r) ||
		r == 0xfffd || r == 0x0340 || r == 0x0341 ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x206a && r <= 0x206f)
}
func isRandAL(r rune) bool    { return unicode.Is(unicode.Arabic, r) || unicode.Is(unicode.Hebrew, r) }
func firstRune(s string) rune { r, _ := utf8.DecodeRuneInString(s); return r }
func lastRune(s string) rune  { r, _ := utf8.DecodeLastRuneInString(s); return r }
