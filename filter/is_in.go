package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// IsIn matches when the value is contained in the given items (slice or map keys).
func IsIn(items any) *filtertype.IsContainedInMatcher {
	return &filtertype.IsContainedInMatcher{Items: items}
}
