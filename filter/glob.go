package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// GlobMatch matches the value against a glob pattern.
func GlobMatch(pattern string) *filtertype.Glob {
	return &filtertype.Glob{Pattern: pattern}
}
