package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Matches matches the value against a regular expression.
func Matches(expression string) *filtertype.RegExp {
	return &filtertype.RegExp{Expression: expression}
}
