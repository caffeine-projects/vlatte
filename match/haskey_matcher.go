package match

import (
	"strings"

	"github.com/ohler55/ojg/jp"

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
	doc, ok := jsonDoc(arg.Value())
	if !ok {
		return mismatch(strings.Join([]string{"HasKey(", m.Path, ") Value is not JSON"}, ""))
	}

	results := m.Expr.Get(doc)

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
