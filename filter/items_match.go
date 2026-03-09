package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// ItemsMatch matches when the value slice has the same elements as expected (order-independent).
func ItemsMatch(items any) vlatte.Filter {
	return &filtertype.ItemsMatchMatcher{Expected: items}
}
