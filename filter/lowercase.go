package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// ToLower lowers the incoming request value case before submitting it to the provided matcher.
func ToLower(matcher vlatte.Filter) *filtertype.LowerCaseMatcher {
	return &filtertype.LowerCaseMatcher{Matcher: matcher}
}
