package match

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

// xmlKind is the kind of a node of an XML document.
type xmlKind int

const (
	_xmlDocument xmlKind = iota + 1
	_xmlElement
	_xmlText
	_xmlComment
	_xmlProcInst
)

// xmlNode is a node of an XML document, as written: its text keeps its white space, its names their prefixes.
type xmlNode struct {
	kind     xmlKind
	name     xmlName // an element's; a processing instruction's target, as local
	attrs    []xmlAttr
	children []*xmlNode
	parent   *xmlNode
	text     string // the data of text, a comment or a processing instruction
}

type xmlAttr struct {
	name  xmlName
	value string
	decl  bool // whether it declares a namespace, unless namespaces are ignored
}

// xmlName is the name of an element or an attribute: its namespace URI, its prefix and its local name. When
// namespaces are ignored, local is the name as written, with its prefix, and the others are empty.
type xmlName struct {
	space  string
	prefix string
	local  string
}

var (
	errXMLNoRoot    = errors.New("no root element")
	errXMLManyRoots = errors.New("more than one root element")
	errXMLOutside   = errors.New("text outside the root element")
)

// parseXML reads an XML document into its tree, whose root is the document node. Unless mode ignores namespaces,
// each prefix must be bound by a declaration.
func parseXML(data []byte, mode XMLNamespaceMode) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel

	doc := &xmlNode{kind: _xmlDocument}
	open := doc        // the innermost element open, or the document
	var raw []xml.Name // the names of the open elements, as written, to check each end matches its start
	var root *xmlNode

	add := func(n *xmlNode) {
		n.parent = open
		open.children = append(open.children, n)
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
				return nil, errXMLManyRoots
			}

			e, err := newXMLElement(t, mode, open)
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

			open, raw = open.parent, raw[:len(raw)-1]
		case xml.CharData:
			if open == doc {
				if len(bytes.Trim(t, _xmlSpace)) > 0 {
					return nil, errXMLOutside
				}

				continue
			}

			// Character data next to text, such as a CDATA section, is more of the same text.
			if last := len(open.children) - 1; last >= 0 && open.children[last].kind == _xmlText {
				open.children[last].text += string(t)
				continue
			}

			add(&xmlNode{kind: _xmlText, text: string(t)})
		case xml.Comment:
			add(&xmlNode{kind: _xmlComment, text: string(t)})
		case xml.ProcInst:
			if t.Target != "xml" {
				add(&xmlNode{kind: _xmlProcInst, name: xmlName{local: t.Target}, text: string(t.Inst)})
			}
		}
	}

	if open != doc {
		return nil, fmt.Errorf("element <%s> is not closed", qualified(raw[len(raw)-1]))
	}

	if root == nil {
		return nil, errXMLNoRoot
	}

	return doc, nil
}

// newXMLElement returns the element a start tag opens inside parent.
func newXMLElement(t xml.StartElement, mode XMLNamespaceMode, parent *xmlNode) (*xmlNode, error) {
	e := &xmlNode{kind: _xmlElement, parent: parent}

	if mode == XMLNamespacesIgnored {
		e.name = xmlName{local: qualified(t.Name)}
		for _, a := range t.Attr {
			e.attrs = append(e.attrs, xmlAttr{name: xmlName{local: qualified(a.Name)}, value: a.Value})
		}

		return e, nil
	}

	for _, a := range t.Attr {
		if prefix, ok := declares(a.Name); ok {
			e.attrs = append(e.attrs, xmlAttr{name: xmlName{prefix: "xmlns", local: prefix}, value: a.Value, decl: true})
		}
	}

	space, ok := e.namespace(t.Name.Space)
	if !ok {
		return nil, fmt.Errorf("the prefix %q of <%s> is not bound to a namespace", t.Name.Space, qualified(t.Name))
	}

	e.name = xmlName{space: space, prefix: t.Name.Space, local: t.Name.Local}

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

		e.attrs = append(e.attrs, xmlAttr{name: xmlName{space: attrSpace, prefix: a.Name.Space, local: a.Name.Local}, value: a.Value})
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

// namespace returns the URI prefix is bound to at the element: by its own declarations, or else by the innermost of
// its ancestors'.
func (n *xmlNode) namespace(prefix string) (string, bool) {
	for e := n; e != nil; e = e.parent {
		for _, a := range e.attrs {
			if a.decl && a.name.local == prefix {
				return a.value, true
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

// root returns the root element of a document.
func (n *xmlNode) root() *xmlNode {
	i := slices.IndexFunc(n.children, func(c *xmlNode) bool { return c.kind == _xmlElement })
	return n.children[i]
}

// qualified returns a name as written: prefix:local.
func qualified(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}

	return n.Space + ":" + n.Local
}

// trimXMLSpace trims the white space of XML off text.
func trimXMLSpace(s string) string {
	return strings.Trim(s, _xmlSpace)
}
