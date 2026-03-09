package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// LessThanOrEqual passes if the value is lower than or equal to the given value.
func LessThanOrEqual(expected float64) vlatte.Filter {
	return &filtertype.LessOrEqualMatcher{Expected: expected}
}

// Lte is an alias for LessThanOrEqual.
func Lte(expected float64) vlatte.Filter {
	return &filtertype.LessOrEqualMatcher{Expected: expected}
}
