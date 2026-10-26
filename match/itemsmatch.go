package match

import (
	"github.com/caffeine-projects/vlatte"
)

// ItemsMatch matches a list holding items in any order, and nothing else: each item of the list pairs with its own
// item of items. An item of items that is a [vlatte.Matcher] pairs with the list items it matches, such as
// ItemsMatch([]vlatte.Matcher{Equal(1), HasPrefix("a")}); any other item pairs with the list items equal to it, by
// Equal's rule.
func ItemsMatch[T any](items []T) vlatte.Matcher {
	return &itemsMatchMatcher{Expected: anys(items)}
}
