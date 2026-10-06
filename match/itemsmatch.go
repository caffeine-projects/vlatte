package match

import (
	"github.com/caffeine-projects/vlatte"
)

// ItemsMatch matches a list holding items in any order: each item of the list equals its own item of items,
// by Equal's rule.
func ItemsMatch[T any](items []T) vlatte.Matcher {
	return &itemsMatchMatcher{Expected: anys(items)}
}
