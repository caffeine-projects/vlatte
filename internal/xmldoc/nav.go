package xmldoc

import (
	"encoding/xml"
	"slices"
	"strings"

	"github.com/antchfx/xpath"
)

var _ xpath.NodeNavigator = (*Nav)(nil)

// Nav navigates the tree of an XML document for XPath, as XPath 1.0 sees it: the document, its elements, their
// attributes, without namespace declarations, its text, white space included, and its comments. The xpath module has
// no node type for processing instructions, so it doesn't see them.
type Nav struct {
	node *Node
	attr int // the index of the attribute in node.Attrs, or -1 for the node itself
}

// NewNav returns a navigator on the document node of a tree.
func NewNav(doc *Node) *Nav {
	return &Nav{node: doc, attr: -1}
}

// Node returns the node the navigator is on, or the element of the attribute it is on.
func (x *Nav) Node() *Node { return x.node }

// visible reports whether XPath sees a node.
func visible(n *Node) bool {
	return n.Kind != ProcInst
}

// NodeType implements xpath.NodeNavigator.
func (x *Nav) NodeType() xpath.NodeType {
	if x.attr >= 0 {
		return xpath.AttributeNode
	}

	switch x.node.Kind {
	case Document:
		return xpath.RootNode
	case Text:
		return xpath.TextNode
	case Comment:
		return xpath.CommentNode
	default:
		return xpath.ElementNode
	}
}

// LocalName implements xpath.NodeNavigator.
func (x *Nav) LocalName() string { return x.name().Local }

// Prefix implements xpath.NodeNavigator: the prefix as written.
func (x *Nav) Prefix() string { return x.name().Prefix }

// NamespaceURL returns the namespace URI of the name, which a prefix that XPathNamespace binds selects by.
func (x *Nav) NamespaceURL() string { return x.name().Space }

// Value implements xpath.NodeNavigator: the string-value of XPath 1.0. An element's and the document's is the text
// they hold, at any depth.
func (x *Nav) Value() string {
	switch {
	case x.attr >= 0:
		return x.node.Attrs[x.attr].Value
	case x.node.Kind == Document || x.node.Kind == Element:
		var b strings.Builder
		writeText(&b, x.node)

		return b.String()
	default:
		return x.node.Text
	}
}

func writeText(b *strings.Builder, n *Node) {
	for _, c := range n.Children {
		switch c.Kind {
		case Text:
			b.WriteString(c.Text)
		case Element:
			writeText(b, c)
		}
	}
}

// Copy implements xpath.NodeNavigator.
func (x *Nav) Copy() xpath.NodeNavigator {
	c := *x
	return &c
}

// MoveToRoot implements xpath.NodeNavigator.
func (x *Nav) MoveToRoot() {
	for x.node.Parent != nil {
		x.node = x.node.Parent
	}

	x.attr = -1
}

// MoveToParent implements xpath.NodeNavigator: an attribute's parent is its element.
func (x *Nav) MoveToParent() bool {
	if x.attr >= 0 {
		x.attr = -1
		return true
	}

	if x.node.Parent == nil {
		return false
	}

	x.node = x.node.Parent

	return true
}

// MoveToNextAttribute implements xpath.NodeNavigator.
func (x *Nav) MoveToNextAttribute() bool {
	if x.node.Kind != Element {
		return false
	}

	for i := x.attr + 1; i < len(x.node.Attrs); i++ {
		if !x.node.Attrs[i].Decl {
			x.attr = i
			return true
		}
	}

	return false
}

// MoveToChild implements xpath.NodeNavigator.
func (x *Nav) MoveToChild() bool {
	if x.attr >= 0 {
		return false
	}

	i := slices.IndexFunc(x.node.Children, visible)
	if i < 0 {
		return false
	}

	x.node = x.node.Children[i]

	return true
}

// MoveToFirst implements xpath.NodeNavigator: to the first sibling.
func (x *Nav) MoveToFirst() bool {
	if x.attr >= 0 || x.node.Parent == nil {
		return false
	}

	x.node = x.node.Parent.Children[slices.IndexFunc(x.node.Parent.Children, visible)]

	return true
}

// MoveToNext implements xpath.NodeNavigator: to the next sibling.
func (x *Nav) MoveToNext() bool {
	return x.moveSibling(1)
}

// MoveToPrevious implements xpath.NodeNavigator: to the previous sibling.
func (x *Nav) MoveToPrevious() bool {
	return x.moveSibling(-1)
}

// MoveTo implements xpath.NodeNavigator: to where other is.
func (x *Nav) MoveTo(other xpath.NodeNavigator) bool {
	o, ok := other.(*Nav)
	if ok {
		*x = *o
	}

	return ok
}

// name returns the name of the node or attribute: an element's or an attribute's, and nothing for other nodes.
func (x *Nav) name() Name {
	switch {
	case x.attr >= 0:
		return x.node.Attrs[x.attr].Name
	case x.node.Kind == Element:
		return x.node.Name
	default:
		return Name{}
	}
}

func (x *Nav) moveSibling(step int) bool {
	if x.attr >= 0 || x.node.Parent == nil {
		return false
	}

	siblings := x.node.Parent.Children
	for i := x.node.Index + step; i >= 0 && i < len(siblings); i += step {
		if visible(siblings[i]) {
			x.node = siblings[i]
			return true
		}
	}

	return false
}

// ElementXML returns the XML of an element, with the namespace declarations its ancestors make that it doesn't make
// itself, the innermost of each prefix, so its names mean in it what they mean in the document.
func ElementXML(e *Node) string {
	declared := make(map[string]bool)
	for _, a := range e.Attrs {
		if a.Decl {
			declared[a.Name.Local] = true
		}
	}

	var inherited []Attr

	for ancestor := e.Parent; ancestor != nil; ancestor = ancestor.Parent {
		for _, a := range ancestor.Attrs {
			if a.Decl && !declared[a.Name.Local] {
				declared[a.Name.Local] = true
				inherited = append(inherited, a)
			}
		}
	}

	var b strings.Builder
	writeXML(&b, e, inherited)

	return b.String()
}

// _textEscaper escapes what text can't hold as is. Line breaks and tabs stay as written, as text matchers expect them.
var _textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// writeXML writes a node as XML, with more attributes on an element: an element has its end tag, even when empty, and
// CDATA sections are escaped text.
func writeXML(b *strings.Builder, n *Node, more []Attr) {
	switch n.Kind {
	case Text:
		b.WriteString(_textEscaper.Replace(n.Text))
	case Comment:
		b.WriteString("<!--" + n.Text + "-->")
	case ProcInst:
		b.WriteString("<?" + n.Name.Local + " " + n.Text + "?>")
	default:
		name := writtenName(n.Name)

		b.WriteString("<" + name)

		for _, a := range append(slices.Clone(n.Attrs), more...) {
			b.WriteString(" " + writtenName(a.Name) + `="`)
			_ = xml.EscapeText(b, []byte(a.Value))
			b.WriteString(`"`)
		}

		b.WriteString(">")

		for _, c := range n.Children {
			writeXML(b, c, nil)
		}

		b.WriteString("</" + name + ">")
	}
}

// writtenName returns a name as written. A declaration of the default namespace is xmlns.
func writtenName(n Name) string {
	switch {
	case n.Prefix == "":
		return n.Local
	case n.Local == "":
		return n.Prefix
	default:
		return n.Prefix + ":" + n.Local
	}
}
