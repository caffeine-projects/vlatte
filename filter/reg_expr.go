package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Matches matches the value against a regular expression.
func Matches(expression string) *filtertype.RegExpMatcher {
	return &filtertype.RegExpMatcher{Expression: expression}
}
