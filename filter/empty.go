package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Empty matches when the value is empty (slice, map, or string).
func Empty() *filtertype.EmptyMatcher {
	return &filtertype.EmptyMatcher{}
}
