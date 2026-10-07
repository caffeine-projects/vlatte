package match

import (
	"fmt"
	"strings"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
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
	doc, isJSON := jsonDoc(arg.Value())
	if !isJSON {
		return mismatch(strings.Join([]string{"Field(", m.Path, ") Value is not JSON"}, ""))
	}

	results := m.Expr.Get(doc)

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

// jsonDoc returns the document a value holds: text, such as a request body, read as JSON the way documents are
// read, or any other value as it is.
func jsonDoc(v any) (any, bool) {
	text, ok := value.Text(v)
	if !ok {
		return v, true
	}

	doc, err := value.DecodeJSONLenient([]byte(text))

	return doc, err == nil
}
