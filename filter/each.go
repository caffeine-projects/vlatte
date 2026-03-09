package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Each applies the given matcher on all items of the incoming request value.
func Each(matcher vlatte.Filter) *filtertype.EachMatcher {
	return &filtertype.EachMatcher{Matcher: matcher}
}
