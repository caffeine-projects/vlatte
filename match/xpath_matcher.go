package match

import (
	"fmt"
	"strings"
	"sync"

	"github.com/antchfx/xpath"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/internal/xmldoc"
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

	// mu guards compiled: an expression keeps state as it evaluates, and mocks match requests at once.
	mu       sync.Mutex
	compiled *xpath.Expr
}

// Name implements vlatte.Matcher.
func (m *xpathMatcher) Name() string { return "XPath" }

// Match implements vlatte.Matcher.
func (m *xpathMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	text, ok := value.Text(arg.Value())
	if !ok {
		return mismatch(strings.Join([]string{"XPath(", m.Expr, ") Value is not text"}, ""))
	}

	doc, err := xmldoc.Parse([]byte(text), false)
	if err != nil {
		return mismatch(strings.Join([]string{"XPath(", m.Expr, ") Value is not XML"}, ""))
	}

	ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), m.evaluate(doc)))
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
	if nav, ok := n.(*xmldoc.Nav); ok && n.NodeType() == xpath.ElementNode {
		return xmldoc.ElementXML(nav.Node())
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

// evaluate returns what the expression selects in a document: nil for no node, the node for one, and a list for more,
// or the number, string or bool it computes.
func (m *xpathMatcher) evaluate(doc *xmldoc.Node) any {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := m.compiled.Evaluate(xmldoc.NewNav(doc))

	r, isNodes := result.(*xpath.NodeIterator)
	if !isNodes {
		return result
	}

	var nodes []any
	for r.MoveNext() {
		nodes = append(nodes, nodeValue(r.Current()))
	}

	switch len(nodes) {
	case 0:
		return nil
	case 1:
		return nodes[0]
	default:
		return nodes
	}
}
