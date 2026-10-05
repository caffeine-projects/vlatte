package match

import (
	"github.com/caffeine-projects/vlatte"
)

// HasSuffix matches when the value has the given suffix.
func HasSuffix(suffix string) vlatte.Matcher {
	return &hasSuffixMatcher{Suffix: suffix}
}
