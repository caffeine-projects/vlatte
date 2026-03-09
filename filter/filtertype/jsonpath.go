package filtertype

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// JSONPathMatcher applies the inner matcher to the JSON value at the given path.
type JSONPathMatcher struct {
	Path    string
	Expr    jp.Expr
	Matcher vlatte.Filter
	Name    string
}

// Match implements vlatte.Filter.
func (m *JSONPathMatcher) Match(v any) (bool, vlatte.Reason, error) {
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return internal.Error(fmt.Errorf("field: %s: error parsing json: %w", m.Path, err))
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
		ok, _, err = m.Matcher.Match(nil)
	case 1:
		ok, _, err = m.Matcher.Match(results[0])
	default:
		ok, _, err = m.Matcher.Match(results)
	}

	if err != nil {
		return internal.Error(fmt.Errorf("field: %w", err))
	}

	if ok {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"Field(", m.Path, ") value did not match"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *JSONPathMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("jsonPath", m.Path, Describe(m.Matcher))
}
