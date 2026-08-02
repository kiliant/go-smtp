// Package unicodenorm provides the small normalisation boundary needed by
// SASLprep. It is kept internal so a future generated Unicode table can replace
// this implementation without affecting the public client API.
package unicodenorm

import "strings"

// NFKC returns the compatibility-normalized form used by RFC 4013. The RFC
// 3454 stringprep tables are deliberately fixed at Unicode 3.2; compatibility
// mappings that matter to stringprep are kept here rather than delegated to a
// caller's locale.
func NFKC(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0x00a0 || r == 0x1680 || (r >= 0x2000 && r <= 0x200a) || r == 0x202f || r == 0x205f || r == 0x3000:
			b.WriteByte(' ')
		case r >= 0xff01 && r <= 0xff5e:
			b.WriteRune(r - 0xfee0)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
