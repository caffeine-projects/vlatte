package filter

import (
	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// HasKey matches when the value (map or struct) has the given key/path.
func HasKey(path string) *filtertype.HasKeyMatcher {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(err)
	}
	return &filtertype.HasKeyMatcher{Path: path, Expr: x}
}
