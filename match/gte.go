package match

import (
	"github.com/caffeine-projects/vlatte"
)

// GreaterThanOrEqual matches a number, or text that reads as one, greater than or equal to expected.
// Numbers compare by their exact value, whatever their Go types; other values don't match.
func GreaterThanOrEqual[N Number](expected N) vlatte.Matcher {
	return &greaterOrEqualMatcher{Expected: expected}
}

// GTE is an alias for GreaterThanOrEqual.
func GTE[N Number](expected N) vlatte.Matcher {
	return GreaterThanOrEqual(expected)
}
