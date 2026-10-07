package match

import (
	"regexp"

	"github.com/caffeine-projects/vlatte"
)

// RegExpr matches text the regular expression matches; other values don't match.
// It panics when expression isn't a regular expression.
func RegExpr(expression string) vlatte.Matcher {
	return &regExpMatcher{Expression: expression, rg: regexp.MustCompile(expression)}
}
