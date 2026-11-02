// Package slug turns text, such as the name of a mock, into a part of a file name that every system accepts.
package slug

import (
	"strings"
	"unicode/utf8"
)

// Make returns s as letters, digits, '.', '_' and '-': each run of anything else becomes one '-'. It is at most limit
// bytes long, never starts or ends with '-' or '.', and is "" when s holds nothing else.
func Make(s string, limit int) string {
	var b strings.Builder

	dash := false
	for _, r := range s {
		if b.Len() >= limit {
			break
		}

		if r < utf8.RuneSelf && (isAlnum(byte(r)) || r == '.' || r == '_' || r == '-') {
			b.WriteRune(r)
			dash = r == '-'

			continue
		}

		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}

	return strings.Trim(b.String(), "-.")
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}
