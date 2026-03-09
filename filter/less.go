package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// LessThan passes if the value is lower than the given value.
func LessThan(expected float64) *filtertype.Less {
	return &filtertype.Less{Expected: expected}
}

// Lt is an alias for LessThan.
func Lt(expected float64) *filtertype.Less {
	return &filtertype.Less{Expected: expected}
}
