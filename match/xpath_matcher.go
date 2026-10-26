package match

import (
	"encoding/xml"
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
		return elementXML(nav.Current())
	}

	return n.Value()
}

// elementXML returns the XML of an element with the namespace declarations its ancestors make, the innermost of each
// prefix, so its names mean in it what they mean in the document.
func elementXML(n *xmlquery.Node) string {
	out := n.OutputXML(true)

	declared := make(map[string]bool)
	for _, a := range n.Attr {
		if prefix, ok := declaresPrefix(a); ok {
			declared[prefix] = true
		}
	}

	var decls strings.Builder

	for ancestor := n.Parent; ancestor != nil; ancestor = ancestor.Parent {
		for _, a := range ancestor.Attr {
			prefix, ok := declaresPrefix(a)
			if !ok || declared[prefix] {
				continue
			}

			declared[prefix] = true

			decls.WriteString(" " + qualified(a.Name) + `="`)
			_ = xml.EscapeText(&decls, []byte(a.Value))
			decls.WriteString(`"`)
		}
	}

	// The declarations go after the element's name, which ends the start tag's first word.
	end := strings.IndexAny(out, " \t\r\n/>")
	if decls.Len() == 0 || end < 0 {
		return out
	}

	return out[:end] + decls.String() + out[end:]
}

// declaresPrefix reports whether an attribute declares a namespace, and the prefix it binds: "" for the default one.
func declaresPrefix(a xmlquery.Attr) (string, bool) {
	switch {
	case a.Name.Space == "" && a.Name.Local == "xmlns":
		return "", true
	case a.Name.Space == "xmlns":
		return a.Name.Local, true
	default:
		return "", false
	}
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
