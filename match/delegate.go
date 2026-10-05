package match

import (
	"github.com/caffeine-projects/vlatte"
)

func Delegate(name string, predicate func(arg vlatte.MatcherArg) (bool, error)) vlatte.Matcher {
	return vlatte.MatcherFunc(name, predicate)
}
