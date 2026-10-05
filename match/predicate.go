package match

import "github.com/caffeine-projects/vlatte"

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) vlatte.Matcher {
	return vlatte.MatcherFunc("predicate", func(arg vlatte.MatcherArg) (bool, error) {
		if predicate(arg.Value()) {
			return true, nil
		}

		return false, nil
	})
}
