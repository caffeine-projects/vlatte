package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GreaterThanOrEqual passes if the value is greater than or equal to the given value.
func GreaterThanOrEqual(expected float64) *filtertype.GreaterOrEqual {
	return &filtertype.GreaterOrEqual{Expected: expected}
}

// GTE is an alias for GreaterThanOrEqual.
func GTE(expected float64) *filtertype.GreaterOrEqual {
	return &filtertype.GreaterOrEqual{Expected: expected}
}
