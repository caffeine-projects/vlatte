package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Item applies matcher to the item of a list at index. A value that isn't a list, or is too short, doesn't match.
// It panics when index is negative.
func Item(index int, matcher vlatte.Matcher) vlatte.Matcher {
	if index < 0 {
		panic(fmt.Errorf("match: item: the index must be 0 or more, not %d", index))
	}

	return &itemMatcher{Index: index, Matcher: matcher}
}
