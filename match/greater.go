package match

import (
	"github.com/caffeine-projects/vlatte"
)

// GreaterThan matches when the value is greater than expected.
func GreaterThan(expected float64) vlatte.Matcher {
	return &greaterMatcher{Expected: expected}
}

// GT is an alias for GreaterThan.
func GT(expected float64) vlatte.Matcher {
	return &greaterMatcher{Expected: expected}
}
