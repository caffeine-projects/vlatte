package match

import (
	"github.com/caffeine-projects/vlatte"
)

// GreaterThanOrEqual passes if the value is greater than or equal to the given value.
func GreaterThanOrEqual(expected float64) vlatte.Matcher {
	return &greaterOrEqualMatcher{Expected: expected}
}

// GTE is an alias for GreaterThanOrEqual.
func GTE(expected float64) vlatte.Matcher {
	return &greaterOrEqualMatcher{Expected: expected}
}
