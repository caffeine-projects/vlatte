package match

import (
	"github.com/caffeine-projects/vlatte"
)

// ItemsMatch matches when the value slice has the same elements as expected (order-independent).
func ItemsMatch(items any) vlatte.Matcher {
	return &itemsMatchMatcher{Expected: items}
}
