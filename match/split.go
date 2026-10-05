package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Split splits the value by separator and matches with the inner matcher.
func Split(separator string, matcher vlatte.Matcher) vlatte.Matcher {
	return &splitMatcher{Separator: separator, Matcher: matcher}
}
