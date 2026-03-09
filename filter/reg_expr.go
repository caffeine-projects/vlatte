package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Matches matches the value against a regular expression.
func Matches(expression string) vlatte.Filter {
	return &filtertype.RegExpMatcher{Expression: expression}
}
