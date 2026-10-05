package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Each applies the given matcher on all items of the incoming request value.
func Each(matcher vlatte.Matcher) vlatte.Matcher {
	return &eachMatcher{Matcher: matcher}
}
