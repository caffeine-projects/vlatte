package match

import (
	"fmt"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
)

// JSONPath applies the provided matcher to the JSON field value in the given path. It panics if path is not a valid
// JSONPath expression.
func JSONPath(path string, matcher vlatte.Matcher) vlatte.Matcher {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}
	return &jsonPathMatcher{
		Path:    path,
		Expr:    x,
		Matcher: matcher,
	}
}

// Field is an alias for [JSONPath]. It panics if path is not a valid JSONPath expression.
func Field(path string, matcher vlatte.Matcher) vlatte.Matcher {
	return JSONPath(path, matcher)
}
