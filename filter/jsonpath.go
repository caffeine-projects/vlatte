package filter

import (
	"fmt"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// JSONPath applies the provided matcher to the JSON field value in the given path.
func JSONPath(path string, matcher vlatte.Filter) vlatte.Filter {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}
	return &filtertype.JSONPathMatcher{
		Path:    path,
		Expr:    x,
		Matcher: matcher,
		Name:    "JSONPath",
	}
}

// Field is an alias for JSONPath.
func Field(path string, matcher vlatte.Filter) vlatte.Filter {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}
	return &filtertype.JSONPathMatcher{
		Path:    path,
		Expr:    x,
		Matcher: matcher,
		Name:    "Field",
	}
}
