package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Not inverts the result of the given matcher.
func Not(matcher vlatte.Filter) vlatte.Filter {
	return &filtertype.NotMatcher{Matcher: matcher}
}
