package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Peek runs an action on the value then matches with the inner matcher.
func Peek(matcher vlatte.Matcher, action func(v any) error) vlatte.Matcher {
	return &peekMatcher{Matcher: matcher, Action: action}
}
