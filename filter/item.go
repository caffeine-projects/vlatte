package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Item matches a specific array/slice item with the inner matcher.
func Item(index int, matcher vlatte.Filter) *filtertype.ItemMatcher {
	return &filtertype.ItemMatcher{Index: index, Matcher: matcher}
}
