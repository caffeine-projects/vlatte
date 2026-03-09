package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Some matches when at least one element matches the inner matcher.
func Some(matcher vlatte.Filter) *filtertype.SomeMatcher {
	return &filtertype.SomeMatcher{Matcher: matcher}
}
