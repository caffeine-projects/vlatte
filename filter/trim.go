package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Trim trims spaces and matches with the inner matcher.
func Trim(matcher vlatte.Filter) *filtertype.TrimMatcher {
	return &filtertype.TrimMatcher{Matcher: matcher}
}
