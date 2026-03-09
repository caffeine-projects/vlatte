package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GreaterThanOrEqual passes if the value is greater than or equal to the given value.
func GreaterThanOrEqual(expected float64) vlatte.Filter {
	return &filtertype.GreaterOrEqualMatcher{Expected: expected}
}

// Gte is an alias for GreaterThanOrEqual.
func Gte(expected float64) vlatte.Filter {
	return &filtertype.GreaterOrEqualMatcher{Expected: expected}
}
