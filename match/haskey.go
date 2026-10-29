package match

import (
	"fmt"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
)

// HasKey matches when the value (map or struct) has the given key/path. It panics if path is not a valid JSONPath
// expression.
func HasKey(path string) vlatte.Matcher {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("match: haskey: the path %s is invalid: %w", path, err))
	}
	return &hasKeyMatcher{Path: path, Expr: x}
}
