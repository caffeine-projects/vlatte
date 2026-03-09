package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GreaterThan matches when the value is greater than expected.
func GreaterThan(expected float64) *filtertype.Greater {
	return &filtertype.Greater{Expected: expected}
}

// Gt is an alias for GreaterThan.
func Gt(expected float64) *filtertype.Greater {
	return &filtertype.Greater{Expected: expected}
}
