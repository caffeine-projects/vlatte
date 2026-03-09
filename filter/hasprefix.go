package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// HasPrefix matches when the value has the given prefix.
func HasPrefix(prefix string) vlatte.Filter {
	return &filtertype.HasPrefixMatcher{Prefix: prefix}
}
