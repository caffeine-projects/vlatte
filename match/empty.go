package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Empty matches when the value is empty (slice, map, or string).
func Empty() vlatte.Matcher {
	return &emptyMatcher{}
}
