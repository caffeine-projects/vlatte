package match

import (
	"github.com/caffeine-projects/vlatte"
)

// Len matches when the value has the given length (slice, map, or string).
func Len(length int) vlatte.Matcher {
	return &lenMatcher{Length: length}
}
