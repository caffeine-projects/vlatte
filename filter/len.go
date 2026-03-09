package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Len matches when the value has the given length (slice, map, or string).
func Len(length int) *filtertype.LenMatcher {
	return &filtertype.LenMatcher{Length: length}
}
