package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Peek runs an action on the value then matches with the inner matcher.
func Peek(matcher vlatte.Filter, action func(v any) error) vlatte.Filter {
	return &filtertype.PeekMatcher{Matcher: matcher, Action: action}
}
