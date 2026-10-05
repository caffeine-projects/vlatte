package match

import (
	"github.com/caffeine-projects/vlatte"
)

// IsIn matches when the value is contained in the given items (slice or map keys).
func IsIn(items any) vlatte.Matcher {
	return &isInMatcher{Items: items}
}
