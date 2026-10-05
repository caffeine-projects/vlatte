package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Item matches a specific array/slice item with the inner matcher.
func Item(index int, matcher vlatte.Matcher) vlatte.Matcher {
	return &itemMatcher{Index: index, Matcher: matcher}
}
