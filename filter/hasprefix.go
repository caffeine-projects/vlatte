package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// HasPrefix matches when the value has the given prefix.
func HasPrefix(prefix string) *filtertype.HasPrefix {
	return &filtertype.HasPrefix{Prefix: prefix}
}
