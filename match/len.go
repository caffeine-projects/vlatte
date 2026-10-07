package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Len matches text of length bytes, or a list or an object of length items; other values don't match.
// It panics when length is negative.
func Len(length int) vlatte.Matcher {
	if length < 0 {
		panic(fmt.Errorf("match: len: the length must be 0 or more, not %d", length))
	}

	return &lenMatcher{Length: length}
}
