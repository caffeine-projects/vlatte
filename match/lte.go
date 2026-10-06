package match

import (
	"github.com/caffeine-projects/vlatte"
)

// LessThanOrEqual matches a number, or text that reads as one, less than or equal to expected.
// Numbers compare by their exact value, whatever their Go types; other values don't match.
func LessThanOrEqual[N Number](expected N) vlatte.Matcher {
	return &lessOrEqualMatcher{Expected: expected}
}

// LTE is an alias for LessThanOrEqual.
func LTE[N Number](expected N) vlatte.Matcher {
	return LessThanOrEqual(expected)
}
