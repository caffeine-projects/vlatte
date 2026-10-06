package match

import (
	"github.com/caffeine-projects/vlatte"
)

// GreaterThan matches a number, or text that reads as one, greater than expected.
// Numbers compare by their exact value, whatever their Go types; other values don't match.
func GreaterThan[N Number](expected N) vlatte.Matcher {
	return &greaterMatcher{Expected: expected}
}

// GT is an alias for GreaterThan.
func GT[N Number](expected N) vlatte.Matcher {
	return GreaterThan(expected)
}
