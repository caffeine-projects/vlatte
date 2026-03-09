package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// HasSuffix matches when the value has the given suffix.
func HasSuffix(suffix string) vlatte.Filter {
	return &filtertype.HasSuffixMatcher{Suffix: suffix}
}
