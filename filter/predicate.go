package filter

import "github.com/caffeine-projects/vlatte"

// Predicate creates a Matcher function using the given predicate.
func Predicate(predicate func(v any) bool) vlatte.FilterFunc {
	return func(v any) (bool, vlatte.Reason, error) {
		if predicate(v) {
			return true, vlatte.Reason(""), nil
		}

		return false, vlatte.Reason("F() Wrapped predicate did not match"), nil
	}
}

