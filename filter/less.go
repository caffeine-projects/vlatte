package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// LessThan passes if the value is lower than the given value.
func LessThan(expected float64) vlatte.Filter {
	return &filtertype.LessMatcher{Expected: expected}
}

// Lt is an alias for LessThan.
func Lt(expected float64) vlatte.Filter {
	return &filtertype.LessMatcher{Expected: expected}
}
