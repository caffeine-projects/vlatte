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

// xmlElement is an element of an XML document, as EqualXML compares it: comments, processing instructions and the
// doctype are left out, and so is text that is only white space.
type xmlElement struct {
	name     xmlName
	attrs    []xmlAttr // without the namespace declarations, unless namespaces are ignored
	children []xmlNode
}

// xmlNode is a child of an element: an element, or text, trimmed, with its CDATA sections.
type xmlNode struct {
	elem *xmlElement // nil for text
	text string
}

type xmlAttr struct {
	name  xmlName
	value string
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
)

// parseXML reads an XML document. Unless mode ignores namespaces, each prefix must be bound by a declaration.
func parseXML(data []byte, mode XMLNamespaceMode) (*xmlElement, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel

	var (
		root   *xmlElement
		open   []*xmlElement
		raw    []xml.Name          // the names of the open elements, as written, to check each end matches its start
		scopes []map[string]string // the prefixes each open element binds, to their URIs
		text   strings.Builder     // the text since the last element tag
	)

	flush := func() {
		if t := strings.TrimSpace(text.String()); t != "" && len(open) > 0 {
			parent := open[len(open)-1]
			parent.children = append(parent.children, xmlNode{text: t})
		}

		text.Reset()
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
			flush()

			if len(open) == 0 && root != nil {
				return nil, errXMLManyRoots
			}

			e, scope, err := newXMLElement(t, mode, scopes)
			if err != nil {
				return nil, err
			}

			if len(open) == 0 {
				root = e
			} else {
				parent := open[len(open)-1]
				parent.children = append(parent.children, xmlNode{elem: e})
			}

			open = append(open, e)
			raw = append(raw, t.Name)
			scopes = append(scopes, scope)
		case xml.EndElement:
			if len(open) == 0 || raw[len(raw)-1] != t.Name {
				return nil, fmt.Errorf("unexpected end element </%s>", qualified(t.Name))
			}

			flush()

			open, raw, scopes = open[:len(open)-1], raw[:len(raw)-1], scopes[:len(scopes)-1]
		case xml.CharData:
			if len(open) == 0 {
				if len(bytes.TrimSpace(t)) > 0 {
					return nil, errors.New("text outside the root element")
				}

				continue
			}

			text.Write(t)
		}
	}

	if len(open) > 0 {
		return nil, fmt.Errorf("element <%s> is not closed", qualified(raw[len(raw)-1]))
	}

	if root == nil {
		return nil, errXMLNoRoot
	}

	return root, nil
}

// newXMLElement returns the element a start tag opens, and the prefixes it binds.
func newXMLElement(t xml.StartElement, mode XMLNamespaceMode, scopes []map[string]string) (*xmlElement, map[string]string, error) {
	e := &xmlElement{}

	if mode == XMLNamespacesIgnored {
		e.name = xmlName{local: qualified(t.Name)}
		for _, a := range t.Attr {
			e.attrs = append(e.attrs, xmlAttr{name: xmlName{local: qualified(a.Name)}, value: a.Value})
		}

		return e, nil, nil
	}

	scope := make(map[string]string)
	for _, a := range t.Attr {
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			scope[""] = a.Value
		case a.Name.Space == "xmlns":
			scope[a.Name.Local] = a.Value
		}
	}

	resolve := func(prefix string) (string, bool) {
		if uri, ok := scope[prefix]; ok {
			return uri, true
		}

		for _, outer := range slices.Backward(scopes) {
			if uri, ok := outer[prefix]; ok {
				return uri, true
			}
		}

		switch prefix {
		case "":
			return "", true
		case "xml":
			return "http://www.w3.org/XML/1998/namespace", true
		}

		return "", false
	}

	space, ok := resolve(t.Name.Space)
	if !ok {
		return nil, nil, fmt.Errorf("the prefix %q of <%s> is not bound to a namespace", t.Name.Space, qualified(t.Name))
	}

	e.name = xmlName{space: space, prefix: t.Name.Space, local: t.Name.Local}

	for _, a := range t.Attr {
		if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
			continue
		}

		// An attribute without a prefix is in no namespace, whatever the default one.
		var attrSpace string
		if a.Name.Space != "" {
			if attrSpace, ok = resolve(a.Name.Space); !ok {
				return nil, nil, fmt.Errorf("the prefix %q of attribute %s is not bound to a namespace", a.Name.Space, qualified(a.Name))
			}
		}

		e.attrs = append(e.attrs, xmlAttr{name: xmlName{space: attrSpace, prefix: a.Name.Space, local: a.Name.Local}, value: a.Value})
	}

	return e, scope, nil
}

// qualified returns a name as written: prefix:local.
func qualified(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}

	return n.Space + ":" + n.Local
}
