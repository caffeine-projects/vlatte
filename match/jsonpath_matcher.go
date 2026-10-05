package match

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// jsonPathMatcher applies the inner matcher to the JSON value at the given path.
type jsonPathMatcher struct {
	Path    string
	Expr    jp.Expr
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *jsonPathMatcher) Name() string { return "JSONPath" }

// Match implements vlatte.Matcher.
func (m *jsonPathMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return failure(fmt.Errorf("field: %s: error parsing json: %w", m.Path, err))
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
		ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), nil))
	case 1:
		ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), results[0]))
	default:
		ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), results))
	}

	if err != nil {
		return failure(fmt.Errorf("field: %w", err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"Field(", m.Path, ") value did not match"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *jsonPathMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("jsonPath", m.Path, describe(m.Matcher))
}
