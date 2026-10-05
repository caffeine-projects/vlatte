package match

import (
	"github.com/caffeine-projects/vlatte"
)

// RegExpr matches the value against a regular expression.
func RegExpr(expression string) vlatte.Matcher {
	return &regExpMatcher{Expression: expression}
}
