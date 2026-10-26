package match

import (
	"encoding/xml"
	"slices"
	"strings"

	"github.com/antchfx/xpath"
)

var _ xpath.NodeNavigator = (*xmlNav)(nil)

// xmlNav navigates the tree of an XML document for XPath, as XPath 1.0 sees it: the document, its elements, their
// attributes, without namespace declarations, its text, white space included, and its comments. The xpath module has
// no node type for processing instructions, so it doesn't see them.
type xmlNav struct {
	node *xmlNode
	attr int // the index of the attribute in node.attrs, or -1 for the node itself
}

// newXMLNav returns a navigator on the document node of a tree.
func newXMLNav(doc *xmlNode) *xmlNav {
	return &xmlNav{node: doc, attr: -1}
}

// visible reports whether XPath sees a node.
func visible(n *xmlNode) bool {
	return n.kind != _xmlProcInst
}

// NodeType implements xpath.NodeNavigator.
func (x *xmlNav) NodeType() xpath.NodeType {
	if x.attr >= 0 {
		return xpath.AttributeNode
	}

	switch x.node.kind {
	case _xmlDocument:
		return xpath.RootNode
	case _xmlText:
		return xpath.TextNode
	case _xmlComment:
		return xpath.CommentNode
	default:
		return xpath.ElementNode
	}
}

// LocalName implements xpath.NodeNavigator.
func (x *xmlNav) LocalName() string { return x.name().local }

// Prefix implements xpath.NodeNavigator: the prefix as written.
func (x *xmlNav) Prefix() string { return x.name().prefix }

// NamespaceURL returns the namespace URI of the name, which a prefix that XPathNamespace binds selects by.
func (x *xmlNav) NamespaceURL() string { return x.name().space }

// Value implements xpath.NodeNavigator: the string-value of XPath 1.0. An element's and the document's is the text
// they hold, at any depth.
func (x *xmlNav) Value() string {
	switch {
	case x.attr >= 0:
		return x.node.attrs[x.attr].value
	case x.node.kind == _xmlDocument || x.node.kind == _xmlElement:
		var b strings.Builder
		writeText(&b, x.node)

		return b.String()
	default:
		return x.node.text
	}
}

func writeText(b *strings.Builder, n *xmlNode) {
	for _, c := range n.children {
		switch c.kind {
		case _xmlText:
			b.WriteString(c.text)
		case _xmlElement:
			writeText(b, c)
		}
	}
}

// Copy implements xpath.NodeNavigator.
func (x *xmlNav) Copy() xpath.NodeNavigator {
	c := *x
	return &c
}

// MoveToRoot implements xpath.NodeNavigator.
func (x *xmlNav) MoveToRoot() {
	for x.node.parent != nil {
		x.node = x.node.parent
	}

	x.attr = -1
}

// MoveToParent implements xpath.NodeNavigator: an attribute's parent is its element.
func (x *xmlNav) MoveToParent() bool {
	if x.attr >= 0 {
		x.attr = -1
		return true
	}

	if x.node.parent == nil {
		return false
	}

	x.node = x.node.parent

	return true
}

// MoveToNextAttribute implements xpath.NodeNavigator.
func (x *xmlNav) MoveToNextAttribute() bool {
	if x.node.kind != _xmlElement {
		return false
	}

	for i := x.attr + 1; i < len(x.node.attrs); i++ {
		if !x.node.attrs[i].decl {
			x.attr = i
			return true
		}
	}

	return false
}

// MoveToChild implements xpath.NodeNavigator.
func (x *xmlNav) MoveToChild() bool {
	if x.attr >= 0 {
		return false
	}

	i := slices.IndexFunc(x.node.children, visible)
	if i < 0 {
		return false
	}

	x.node = x.node.children[i]

	return true
}

// MoveToFirst implements xpath.NodeNavigator: to the first sibling.
func (x *xmlNav) MoveToFirst() bool {
	if x.attr >= 0 || x.node.parent == nil {
		return false
	}

	x.node = x.node.parent.children[slices.IndexFunc(x.node.parent.children, visible)]

	return true
}

// MoveToNext implements xpath.NodeNavigator: to the next sibling.
func (x *xmlNav) MoveToNext() bool {
	return x.moveSibling(1)
}

// MoveToPrevious implements xpath.NodeNavigator: to the previous sibling.
func (x *xmlNav) MoveToPrevious() bool {
	return x.moveSibling(-1)
}

// MoveTo implements xpath.NodeNavigator: to where other is, in the same document.
func (x *xmlNav) MoveTo(other xpath.NodeNavigator) bool {
	o, ok := other.(*xmlNav)
	if !ok || document(o.node) != document(x.node) {
		return false
	}

	*x = *o

	return true
}

// name returns the name of the node or attribute: an element's or an attribute's, and nothing for other nodes.
func (x *xmlNav) name() xmlName {
	switch {
	case x.attr >= 0:
		return x.node.attrs[x.attr].name
	case x.node.kind == _xmlElement:
		return x.node.name
	default:
		return xmlName{}
	}
}

func (x *xmlNav) moveSibling(step int) bool {
	if x.attr >= 0 || x.node.parent == nil {
		return false
	}

	siblings := x.node.parent.children
	for i := slices.Index(siblings, x.node) + step; i >= 0 && i < len(siblings); i += step {
		if visible(siblings[i]) {
			x.node = siblings[i]
			return true
		}
	}

	return false
}

func document(n *xmlNode) *xmlNode {
	for n.parent != nil {
		n = n.parent
	}

	return n
}

// elementXML returns the XML of an element, with the namespace declarations its ancestors make that it doesn't make
// itself, the innermost of each prefix, so its names mean in it what they mean in the document.
func elementXML(e *xmlNode) string {
	declared := make(map[string]bool)
	for _, a := range e.attrs {
		if a.decl {
			declared[a.name.local] = true
		}
	}

	var inherited []xmlAttr

	for ancestor := e.parent; ancestor != nil; ancestor = ancestor.parent {
		for _, a := range ancestor.attrs {
			if a.decl && !declared[a.name.local] {
				declared[a.name.local] = true
				inherited = append(inherited, a)
			}
		}
	}

	var b strings.Builder
	writeXML(&b, e, inherited)

	return b.String()
}

// writeXML writes a node as XML, with more attributes on an element.
func writeXML(b *strings.Builder, n *xmlNode, more []xmlAttr) {
	switch n.kind {
	case _xmlText:
		_ = xml.EscapeText(b, []byte(n.text))
	case _xmlComment:
		b.WriteString("<!--" + n.text + "-->")
	case _xmlProcInst:
		b.WriteString("<?" + n.name.local + " " + n.text + "?>")
	default:
		name := writtenName(n.name)

		b.WriteString("<" + name)

		for _, a := range append(slices.Clone(n.attrs), more...) {
			b.WriteString(" " + writtenName(a.name) + `="`)
			_ = xml.EscapeText(b, []byte(a.value))
			b.WriteString(`"`)
		}

		if len(n.children) == 0 {
			b.WriteString("/>")
			return
		}

		b.WriteString(">")

		for _, c := range n.children {
			writeXML(b, c, nil)
		}

		b.WriteString("</" + name + ">")
	}
}

// writtenName returns a name as written. A declaration of the default namespace is xmlns.
func writtenName(n xmlName) string {
	switch {
	case n.prefix == "":
		return n.local
	case n.local == "":
		return n.prefix
	default:
		return n.prefix + ":" + n.local
	}
}
