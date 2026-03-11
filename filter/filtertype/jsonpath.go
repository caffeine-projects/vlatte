package filtertype

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// JSONPath applies the inner matcher to the JSON value at the given path.
type JSONPath struct {
	Path    string
	Expr    jp.Expr
	Matcher vlatte.Filter
	Name    string
}

// Match implements vlatte.Filter.
func (m *JSONPath) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return Error(fmt.Errorf("field: %s: error parsing json: %w", m.Path, err))
		}

		results = m.Expr.Get(data)
	default:
		results = m.Expr.Get(vv)
	}

	var (
		ok  bool
		err error
	)

	size := len(results)
	switch size {
	case 0:
		ok, err = m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), nil))
	case 1:
		ok, err = m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), results[0]))
	default:
		ok, err = m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), results))
	}

	if err != nil {
		return Error(fmt.Errorf("field: %w", err))
	}

	if ok {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Field(", m.Path, ") value did not match"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *JSONPath) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("jsonPath", m.Path, Describe(m.Matcher))
}
