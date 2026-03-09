package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Not inverts the result of the given matcher.
func Not(matcher vlatte.Filter) *filtertype.Not {
	return &filtertype.Not{Matcher: matcher}
}
