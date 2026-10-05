package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Not inverts the result of the given matcher.
func Not(matcher vlatte.Matcher) vlatte.Matcher {
	return &notMatcher{Matcher: matcher}
}
