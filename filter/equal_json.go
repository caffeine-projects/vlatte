package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// EqualJSON matches JSON values.
func EqualJSON(expected any) *filtertype.EqualJSONMatcher {
	return &filtertype.EqualJSONMatcher{Expected: expected}
}

// Eqj is an alias for EqualJSON.
func Eqj(expected any) *filtertype.EqualJSONMatcher {
	return &filtertype.EqualJSONMatcher{Expected: expected}
}
