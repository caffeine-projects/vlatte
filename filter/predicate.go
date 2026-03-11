package filter

import "github.com/caffeine-projects/vlatte"

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) vlatte.FilterFunc {
	return func(arg vlatte.FilterArg) (bool, error) {
		if predicate(arg.Value()) {
			return true, nil
		}

		return false, nil
	}
}

