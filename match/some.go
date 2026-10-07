package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Some matches a list with an item that matches matcher; other values don't match.
func Some(matcher vlatte.Matcher) vlatte.Matcher {
	return &someMatcher{Matcher: matcher}
}
