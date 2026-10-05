package match

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// hasKeyMatcher passes if the JSON key at the given path is present.
type hasKeyMatcher struct {
	Path string
	Expr jp.Expr
}

// Name implements vlatte.Matcher.
func (m *hasKeyMatcher) Name() string { return "HasKey" }

// Match implements vlatte.Matcher.
func (m *hasKeyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	var results []any

	switch vv := v.(type) {
	case string:
		data, err := oj.ParseString(vv)
		if err != nil {
			return failure(fmt.Errorf("has_key: %s: error parsing incoming json: %w", m.Path, err))
		}

		results = m.Expr.Get(data)
	default:
		results = m.Expr.Get(vv)
	}

	size := len(results)
	if size == 0 || (size == 1 && results[0] == nil) {
		return mismatch(strings.Join([]string{"HasKey(", m.Path, ") Is not present"}, ""))
	}

	return success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *hasKeyMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasKey", m.Path)
}
