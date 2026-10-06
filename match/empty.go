package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Empty matches nil, and text, a list or an object of no items; other values don't match.
func Empty() vlatte.Matcher {
	return &emptyMatcher{}
}
