package match

import (
	"github.com/caffeine-projects/vlatte"
)

// LessThanOrEqual passes if the value is lower than or equal to the given value.
func LessThanOrEqual(expected float64) vlatte.Matcher {
	return &lessOrEqualMatcher{Expected: expected}
}

// LTE is an alias for LessThanOrEqual.
func LTE(expected float64) vlatte.Matcher {
	return &lessOrEqualMatcher{Expected: expected}
}
