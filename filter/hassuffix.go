package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// HasSuffix matches when the value has the given suffix.
func HasSuffix(suffix string) *filtertype.HasSuffixMatcher {
	return &filtertype.HasSuffixMatcher{Suffix: suffix}
}
