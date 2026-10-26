package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// EqualXML matches text, such as a request body, that is an XML document equal to expected. Documents are equal when
// their elements are, as XML means them rather than as written:
//   - comments, processing instructions and the doctype don't count;
//   - text is trimmed, and text that is only white space, such as indentation between elements, doesn't count;
//   - attributes are equal in any order, and namespace declarations aren't attributes;
//   - names are equal by namespace URI and local name, whatever their prefixes;
//   - child elements and text are equal in order.
//
// [IgnoreChildOrder] and [XMLNamespaces] change the last two. A value that isn't text, or isn't XML, doesn't match.
// EqualXML panics when expected isn't an XML document.
func EqualXML(expected string, opts ...EqualXMLOption) vlatte.Matcher {
	m := &equalXMLMatcher{Expected: expected}
	for _, opt := range opts {
		opt.applyXML(&m.opts)
	}

	want, err := parseXML([]byte(expected), m.opts.namespaces)
	if err != nil {
		panic(fmt.Errorf("match: eqx: %w", err))
	}

	m.want = want

	return m
}

// EQX is an alias for EqualXML.
func EQX(expected string, opts ...EqualXMLOption) vlatte.Matcher {
	return EqualXML(expected, opts...)
}

// EqualXMLOption configures [EqualXML].
type EqualXMLOption interface {
	applyXML(o *xmlOptions)
}

type xmlOptions struct {
	ignoreChildOrder bool
	namespaces       XMLNamespaceMode
}

type ignoreChildOrder struct{}

func (ignoreChildOrder) applyXML(o *xmlOptions) { o.ignoreChildOrder = true }

// IgnoreChildOrder makes [EqualXML] match elements whose child elements and text pair up in any order, as WireMock's
// ignoreOrderOfSameNode does: <a><b>1</b><b>2</b></a> matches <a><b>2</b><b>1</b></a>.
func IgnoreChildOrder() EqualXMLOption { return ignoreChildOrder{} }

// XMLNamespaceMode is how [EqualXML] compares the names of elements and attributes.
type XMLNamespaceMode int

// The ways [EqualXML] reads namespaces.
const (
	// XMLNamespacesByURI compares names by namespace URI and local name, whatever their prefixes: the default.
	XMLNamespacesByURI XMLNamespaceMode = iota
	// XMLNamespacesStrict compares the prefixes of names too.
	XMLNamespacesStrict
	// XMLNamespacesIgnored reads no namespaces: a name is compared as written, prefix included, a prefix needs no
	// declaration, and namespace declarations are attributes like any other.
	XMLNamespacesIgnored
)

// String returns the name of the mode as mock files write it: uri, strict or none.
func (m XMLNamespaceMode) String() string {
	switch m {
	case XMLNamespacesByURI:
		return "uri"
	case XMLNamespacesStrict:
		return "strict"
	case XMLNamespacesIgnored:
		return "none"
	default:
		return fmt.Sprintf("XMLNamespaceMode(%d)", int(m))
	}
}

type xmlNamespaces XMLNamespaceMode

func (m xmlNamespaces) applyXML(o *xmlOptions) { o.namespaces = XMLNamespaceMode(m) }

// XMLNamespaces sets how [EqualXML] compares the names of elements and attributes, as WireMock's namespaceAwareness
// does.
func XMLNamespaces(mode XMLNamespaceMode) EqualXMLOption { return xmlNamespaces(mode) }
