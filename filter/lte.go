package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// LessThanOrEqual passes if the value is lower than or equal to the given value.
func LessThanOrEqual(expected float64) *filtertype.LessOrEqual {
	return &filtertype.LessOrEqual{Expected: expected}
}

// LTE is an alias for LessThanOrEqual.
func LTE(expected float64) *filtertype.LessOrEqual {
	return &filtertype.LessOrEqual{Expected: expected}
}
