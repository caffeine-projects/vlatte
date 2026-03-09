package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Split splits the value by separator and matches with the inner matcher.
func Split(separator string, matcher vlatte.Filter) *filtertype.Split {
	return &filtertype.Split{Separator: separator, Matcher: matcher}
}
