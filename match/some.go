package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Some matches when at least one element matches the inner matcher.
func Some(matcher vlatte.Matcher) vlatte.Matcher {
	return &someMatcher{Matcher: matcher}
}
