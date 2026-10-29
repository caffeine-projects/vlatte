package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Delegate creates a Matcher with the given name that calls predicate with the whole [vlatte.MatcherArg], and
// returns its error as is. Unlike [Function], predicate reads more than the value.
func Delegate(name string, predicate func(arg vlatte.MatcherArg) (bool, error)) vlatte.Matcher {
	return vlatte.MatcherFunc(name, predicate)
}
