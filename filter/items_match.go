package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// ItemsMatch matches when the value slice has the same elements as expected (order-independent).
func ItemsMatch(items any) *filtertype.ItemsMatchMatcher {
	return &filtertype.ItemsMatchMatcher{Expected: items}
}
