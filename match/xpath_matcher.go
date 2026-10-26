package match

import (
	"fmt"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.Matcher             = (*xpathMatcher)(nil)
	_ descriptor.SelfDescriptive = (*xpathMatcher)(nil)
)

// xpathMatcher applies the inner matcher to what the expression selects in the XML document of the value.
type xpathMatcher struct {
	Expr       string
	Matcher    vlatte.Matcher
	namespaces map[string]string
}

// Name implements vlatte.Matcher.
func (m *xpathMatcher) Name() string { return "XPath" }

// Match implements vlatte.Matcher.
func (m *xpathMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	text, ok := value.Text(arg.Value())
	if !ok {
		return mismatch(strings.Join([]string{"XPath(", m.Expr, ") Value is not text"}, ""))
	}

	doc, err := xmlquery.Parse(strings.NewReader(text))
	if err != nil {
		return mismatch(strings.Join([]string{"XPath(", m.Expr, ") Value is not XML"}, ""))
	}

	// An expression keeps state as it evaluates, so each match compiles its own: mocks match requests at once.
	expr, err := xpath.CompileWithNS(m.Expr, m.namespaces)
	if err != nil {
		return failure(fmt.Errorf("xpath: %w", err))
	}

	var result any

	switch r := expr.Evaluate(xmlquery.CreateXPathNavigator(doc)).(type) {
	case *xpath.NodeIterator:
		var nodes []any
		for r.MoveNext() {
			nodes = append(nodes, nodeValue(r.Current()))
		}

		switch len(nodes) {
		case 0:
		case 1:
			result = nodes[0]
		default:
			result = nodes
		}
	default:
		result = r
	}

	ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), result))
	if err != nil {
		return failure(fmt.Errorf("xpath: %w", err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"XPath(", m.Expr, ") value did not match"}, ""))
}

// nodeValue returns what XPath gives the inner matcher for a node it selects: an element as its XML, and any other
// node as its text.
func nodeValue(n xpath.NodeNavigator) any {
	if nav, ok := n.(*xmlquery.NodeNavigator); ok && n.NodeType() == xpath.ElementNode {
		return nav.Current().OutputXML(true)
	}

	return n.Value()
}

// Describe implements descriptor.SelfDescriptive.
func (m *xpathMatcher) Describe() descriptor.Descriptor {
	if len(m.namespaces) == 0 {
		return descriptor.MultiArgs("xpath", m.Expr, describe(m.Matcher))
	}

	namespaces := make(map[string]any, len(m.namespaces))
	for prefix, uri := range m.namespaces {
		namespaces[prefix] = uri
	}

	return descriptor.MultiArgs("xpath", m.Expr, describe(m.Matcher), map[string]any{"namespaces": namespaces})
}
