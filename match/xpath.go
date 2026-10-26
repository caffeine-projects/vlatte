package match

import (
	"fmt"
	"maps"

	"github.com/antchfx/xpath"

	"github.com/caffeine-projects/vlatte"
)

// XPath applies matcher to what expr, an XPath 1.0 expression, selects in an XML document: text, such as a request
// body. matcher gets nil when expr selects nothing, the one result when it selects one, and a list of them when it
// selects more, as [JSONPath] does: Some(m) matches when any of them matches. An element is given as its XML, with the
// namespace declarations its ancestors make, so [EqualXML] can compare it; an attribute or a text node, as its text; a number, a string or a bool that expr
// computes, as it is.
//
// A name in expr selects elements whose name is written the same, prefix included, unless [XPathNamespace] binds its
// prefix to a namespace URI: then it selects by namespace and local name, whatever prefix the document gives them.
// local-name() selects by local name alone: /*[local-name()='order']. The document's nodes are XPath 1.0's: its
// elements, their attributes, its text, white space included, and its comments; processing instructions aren't
// selected. A value that isn't XML doesn't match. XPath panics when expr isn't an XPath expression.
func XPath(expr string, matcher vlatte.Matcher, opts ...XPathOption) vlatte.Matcher {
	var o xpathOptions
	for _, opt := range opts {
		opt.applyXPath(&o)
	}

	compiled, err := xpath.CompileWithNS(expr, o.namespaces)
	if err != nil {
		panic(fmt.Errorf("match: xpath: the expression %s is invalid: %w", expr, err))
	}

	return &xpathMatcher{Expr: expr, Matcher: matcher, namespaces: o.namespaces, compiled: compiled}
}

// XPathOption configures [XPath].
type XPathOption interface {
	applyXPath(o *xpathOptions)
}

type xpathOptions struct {
	namespaces map[string]string // by prefix
}

type xpathNamespace struct {
	prefix string
	uri    string
}

func (n xpathNamespace) applyXPath(o *xpathOptions) {
	o.namespaces = maps.Clone(o.namespaces)
	if o.namespaces == nil {
		o.namespaces = make(map[string]string)
	}

	o.namespaces[n.prefix] = n.uri
}

// XPathNamespace binds prefix, in the expression of [XPath], to the namespace uri, as WireMock's xPathNamespaces does.
func XPathNamespace(prefix, uri string) XPathOption { return xpathNamespace{prefix: prefix, uri: uri} }
