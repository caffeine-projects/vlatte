package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Nil matches when the value is nil.
func Nil() vlatte.Matcher {
	return &isNilMatcher{}
}
