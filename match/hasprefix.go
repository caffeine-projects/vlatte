package match

import (
	"github.com/caffeine-projects/vlatte"
)

// HasPrefix matches when the value has the given prefix.
func HasPrefix(prefix string) vlatte.Matcher {
	return &hasPrefixMatcher{Prefix: prefix}
}
