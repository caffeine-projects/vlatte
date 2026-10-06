package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Each matches a list whose items, or an object whose members, all match matcher; other values don't match.
func Each(matcher vlatte.Matcher) vlatte.Matcher {
	return &eachMatcher{Matcher: matcher}
}
