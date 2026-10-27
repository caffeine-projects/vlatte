// Package xmldoc reads XML documents into trees that keep them as written, and navigates them for XPath.
package xmldoc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/net/html/charset"
)

// _xmlSpace is the white space of XML: space, tab, carriage return and line feed. Other Unicode spaces, such as a
// non-breaking one, are text.
const _xmlSpace = " \t\r\n"

// Kind is the kind of a node of an XML document.
type Kind int

// The kinds of nodes.
const (
	Document Kind = iota + 1
	Element
	Text
	Comment
	ProcInst
)

// Node is a node of an XML document, as written: its text keeps its white space, its names their prefixes.
type Node struct {
	Kind     Kind
	Name     Name // an element's; a processing instruction's target, as local
	Attrs    []Attr
	Children []*Node
	Parent   *Node
	Index    int    // the node's place among its parent's children
	Text     string // the data of text, a comment or a processing instruction
}

// Attr is an attribute of an element.
type Attr struct {
	Name  Name
	Value string
	Decl  bool // whether it declares a namespace, unless namespaces are ignored
}

// Name is the name of an element or an attribute: its namespace URI, its prefix and its local name. When
// namespaces are ignored, Local is the name as written, with its prefix, and the others are empty.
type Name struct {
	Space  string
	Prefix string
	Local  string
}

var (
	errNoRoot    = errors.New("no root element")
	errManyRoots = errors.New("more than one root element")
	errOutside   = errors.New("text outside the root element")
)

// Parse reads an XML document into its tree, whose root is the document node. Unless ignoreNamespaces is set, each
// prefix must be bound by a declaration.
func Parse(data []byte, ignoreNamespaces bool) (*Node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel

	doc := &Node{Kind: Document}
	open := doc        // the innermost element open, or the document
	var raw []xml.Name // the names of the open elements, as written, to check each end matches its start
	var root *Node

	add := func(n *Node) {
		n.Parent, n.Index = open, len(open.Children)
		open.Children = append(open.Children, n)
	}

	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if open == doc && root != nil {
				return nil, errManyRoots
			}

			e, err := newElement(t, ignoreNamespaces, open)
			if err != nil {
				return nil, err
			}

			add(e)

			if open == doc {
				root = e
			}

			open = e
			raw = append(raw, t.Name)
		case xml.EndElement:
			if open == doc || raw[len(raw)-1] != t.Name {
				return nil, fmt.Errorf("unexpected end element </%s>", qualified(t.Name))
			}

			open, raw = open.Parent, raw[:len(raw)-1]
		case xml.CharData:
			if open == doc {
				if len(bytes.Trim(t, _xmlSpace)) > 0 {
					return nil, errOutside
				}

				continue
			}

			// Character data next to text, such as a CDATA section, is more of the same text.
			if last := len(open.Children) - 1; last >= 0 && open.Children[last].Kind == Text {
				open.Children[last].Text += string(t)
				continue
			}

			add(&Node{Kind: Text, Text: string(t)})
		case xml.Comment:
			add(&Node{Kind: Comment, Text: string(t)})
		case xml.ProcInst:
			if t.Target != "xml" {
				add(&Node{Kind: ProcInst, Name: Name{Local: t.Target}, Text: string(t.Inst)})
			}
		}
	}

	if open != doc {
		return nil, fmt.Errorf("element <%s> is not closed", qualified(raw[len(raw)-1]))
	}

	if root == nil {
		return nil, errNoRoot
	}

	return doc, nil
}

// newElement returns the element a start tag opens inside parent.
func newElement(t xml.StartElement, ignoreNamespaces bool, parent *Node) (*Node, error) {
	e := &Node{Kind: Element, Parent: parent}

	if ignoreNamespaces {
		e.Name = Name{Local: qualified(t.Name)}
		for _, a := range t.Attr {
			e.Attrs = append(e.Attrs, Attr{Name: Name{Local: qualified(a.Name)}, Value: a.Value})
		}

		return e, nil
	}

	for _, a := range t.Attr {
		if prefix, ok := declares(a.Name); ok {
			e.Attrs = append(e.Attrs, Attr{Name: Name{Prefix: "xmlns", Local: prefix}, Value: a.Value, Decl: true})
		}
	}

	space, ok := e.namespace(t.Name.Space)
	if !ok {
		return nil, fmt.Errorf("the prefix %q of <%s> is not bound to a namespace", t.Name.Space, qualified(t.Name))
	}

	e.Name = Name{Space: space, Prefix: t.Name.Space, Local: t.Name.Local}

	for _, a := range t.Attr {
		if _, ok := declares(a.Name); ok {
			continue
		}

		// An attribute without a prefix is in no namespace, whatever the default one.
		var attrSpace string
		if a.Name.Space != "" {
			if attrSpace, ok = e.namespace(a.Name.Space); !ok {
				return nil, fmt.Errorf("the prefix %q of attribute %s is not bound to a namespace", a.Name.Space, qualified(a.Name))
			}
		}

		e.Attrs = append(e.Attrs, Attr{Name: Name{Space: attrSpace, Prefix: a.Name.Space, Local: a.Name.Local}, Value: a.Value})
	}

	return e, nil
}

// declares reports whether an attribute's name declares a namespace, and the prefix it binds: "" for the default one.
func declares(n xml.Name) (string, bool) {
	switch {
	case n.Space == "" && n.Local == "xmlns":
		return "", true
	case n.Space == "xmlns":
		return n.Local, true
	default:
		return "", false
	}
}

// Root returns the root element of a document.
func (n *Node) Root() *Node {
	i := slices.IndexFunc(n.Children, func(c *Node) bool { return c.Kind == Element })
	return n.Children[i]
}

// namespace returns the URI prefix is bound to at the element: by its own declarations, or else by the innermost of
// its ancestors'.
func (n *Node) namespace(prefix string) (string, bool) {
	for e := n; e != nil; e = e.Parent {
		for _, a := range e.Attrs {
			if a.Decl && a.Name.Local == prefix {
				return a.Value, true
			}
		}
	}

	switch prefix {
	case "":
		return "", true
	case "xml":
		return "http://www.w3.org/XML/1998/namespace", true
	default:
		return "", false
	}
}

// qualified returns a name as written: prefix:local.
func qualified(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}

	return n.Space + ":" + n.Local
}

// TrimSpace trims the white space of XML off text.
func TrimSpace(s string) string {
	return strings.Trim(s, _xmlSpace)
}
