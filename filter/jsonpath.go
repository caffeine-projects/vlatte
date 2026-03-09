package filter

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type jsonPathMatcher struct {
	path    string
	expr    jp.Expr
	matcher vlatte.Filter
	name    string
}

func (m *jsonPathMatcher) Match(v any) (vlatte.Result, error) {
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("field: %s: error parsing json: %w", m.path, err)
		}

		results = m.expr.Get(data)
	default:
		results = m.expr.Get(vv)
	}

	var r vlatte.Result
	var err error

	size := len(results)
	switch size {
	case 0:
		r, err = m.matcher.Match(nil)
	case 1:
		r, err = m.matcher.Match(results[0])
	default:
		r, err = m.matcher.Match(results)
	}

	if err != nil {
		return vlatte.Result{}, fmt.Errorf("field: %w", err)
	}

	if r.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{
		Message: strings.Join([]string{"Field(", m.path, ") ", r.Message}, ""),
	}, nil
}

func (m *jsonPathMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("jsonPath", m.path, describe(m.matcher))
}

// JSONPath applies the provided matcher to the JSON field value in the given path.
// Example:
//
//	JSONPath("address.city", EqualTo("Santiago"))
func JSONPath(path string, matcher vlatte.Filter) vlatte.Filter {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}

	return &jsonPathMatcher{
		path:    path,
		expr:    x,
		matcher: matcher,
		name:    "JSONPath",
	}
}

// Field is an alias for JSONPath.
// It applies the provided matcher to the JSON field value in the given path.
// Example:
//
//	Field("address.city", EqualTo("Santiago"))
func Field(path string, matcher vlatte.Filter) vlatte.Filter {
	x, err := jp.ParseString(path)
	if err != nil {
		panic(fmt.Errorf("the json path expression %s is invalid: %w", path, err))
	}

	return &jsonPathMatcher{
		path:    path,
		expr:    x,
		matcher: matcher,
		name:    "Field",
	}
}
