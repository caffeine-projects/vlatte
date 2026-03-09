package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// ToUpper uppercases the value and matches with the inner matcher.
func ToUpper(matcher vlatte.Filter) *filtertype.UpperCaseMatcher {
	return &filtertype.UpperCaseMatcher{Matcher: matcher}
}
