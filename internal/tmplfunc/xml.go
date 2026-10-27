package tmplfunc

import (
	"fmt"
	"strconv"

	"github.com/antchfx/xpath"

	"github.com/caffeine-projects/vlatte/internal/xmldoc"
)

// xmlNode is a node xpathNodes selects. Printing it writes its XML: an element's, with the namespace declarations
// it needs, and the text of any other node.
type xmlNode struct {
	// Name is the name of an element or an attribute, prefix included, and empty for other nodes.
	Name string

	// Text is the text the node holds, at any depth.
	Text string

	// Attrs are the attributes of an element, by name, prefix included, without namespace declarations.
	Attrs map[string]string

	xml string
}

// String returns the XML of the node.
func (n xmlNode) String() string { return n.xml }

// evalXPath evaluates expr on the XML document s.
func evalXPath(expr, s string) (any, error) {
	x, err := xpath.Compile(expr)
	if err != nil {
		return nil, err
	}

	doc, err := xmldoc.Parse([]byte(s), false)
	if err != nil {
		return nil, err
	}

	return x.Evaluate(xmldoc.NewNav(doc)), nil
}

// xpathString returns what expr gives in the XML document s, as text: the text of the first node it selects, or ""
// when it selects none, or the number, text or boolean it computes.
func xpathString(expr, s string) (string, error) {
	r, err := evalXPath(expr, s)
	if err != nil {
		return "", err
	}

	switch v := r.(type) {
	case *xpath.NodeIterator:
		if v.MoveNext() {
			return v.Current().Value(), nil
		}

		return "", nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return fmt.Sprint(v), nil
	}
}

// xpathNodes returns the nodes expr selects in the XML document s. An expression that computes a value instead is an
// error.
func xpathNodes(expr, s string) ([]xmlNode, error) {
	r, err := evalXPath(expr, s)
	if err != nil {
		return nil, err
	}

	it, ok := r.(*xpath.NodeIterator)
	if !ok {
		return nil, fmt.Errorf("%q computes a %T, not nodes", expr, r)
	}

	nodes := []xmlNode{}

	for it.MoveNext() {
		if nav, ok := it.Current().(*xmldoc.Nav); ok {
			nodes = append(nodes, newXMLNode(nav))
		}
	}

	return nodes, nil
}

// newXMLNode returns the node a navigator is on.
func newXMLNode(nav *xmldoc.Nav) xmlNode {
	n := xmlNode{Text: nav.Value(), xml: nav.Value()}

	switch nav.NodeType() {
	case xpath.ElementNode:
		n.Name = qualifiedName(nav.Prefix(), nav.LocalName())
		n.xml = xmldoc.ElementXML(nav.Node())
		n.Attrs = make(map[string]string, len(nav.Node().Attrs))

		for _, a := range nav.Node().Attrs {
			if !a.Decl {
				n.Attrs[qualifiedName(a.Name.Prefix, a.Name.Local)] = a.Value
			}
		}
	case xpath.AttributeNode:
		n.Name = qualifiedName(nav.Prefix(), nav.LocalName())
	}

	return n
}

func qualifiedName(prefix, local string) string {
	if prefix == "" {
		return local
	}

	return prefix + ":" + local
}
