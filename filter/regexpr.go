package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// RegExpr matches the value against a regular expression.
func RegExpr(expression string) *filtertype.RegExp {
	return &filtertype.RegExp{Expression: expression}
}
