package filter

import "github.com/caffeine-projects/vlatte"

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) vlatte.FilterFunc {
	return func(v any) (bool, error) {
		if predicate(v) {
			return true, nil
		}

		return false, nil
	}
}

