package match

import (
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/internal/xmldoc"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

var (
	_ vlatte.Matcher         = (*equalXMLMatcher)(nil)
	_ vlatte.SelfDescriptive = (*equalXMLMatcher)(nil)
)

// equalXMLMatcher passes when the value is an XML document equal to the expected one.
type equalXMLMatcher struct {
	Expected string
	want     *xmldoc.Node // the root element
	opts     xmlOptions
}

// Name implements vlatte.Matcher.
func (m *equalXMLMatcher) Name() string { return "EqualXML" }

// Match implements vlatte.Matcher.
func (m *equalXMLMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if text, ok := value.Text(v); ok {
		got, err := xmldoc.Parse([]byte(text), m.opts.namespaces == XMLNamespacesIgnored)
		if err == nil && m.opts.sameElement(m.want, got.Root()) {
			return success()
		}
	}

	return mismatch(strings.Join([]string{"EqualXML(", m.Expected, ") Got: ", anyconv.Stringify(v)}, ""))
}

func (o xmlOptions) sameName(want, got xmldoc.Name) bool {
	switch o.namespaces {
	case XMLNamespacesStrict:
		return want == got
	default:
		return want.Space == got.Space && want.Local == got.Local
	}
}

// xmlItem is a part of an element's content, as EqualXML compares it: an element, or text.
type xmlItem struct {
	elem *xmldoc.Node // nil for text
	text string
}

// content returns what EqualXML compares of an element's children: its elements, and the text between two of them,
// comments and processing instructions left out, trimmed of XML white space, when any is left.
func content(e *xmldoc.Node) []xmlItem {
	var (
		items []xmlItem
		text  strings.Builder
	)

	flush := func() {
		if t := xmldoc.TrimSpace(text.String()); t != "" {
			items = append(items, xmlItem{text: t})
		}

		text.Reset()
	}

	for _, c := range e.Children {
		switch c.Kind {
		case xmldoc.Element:
			flush()

			items = append(items, xmlItem{elem: c})
		case xmldoc.Text:
			text.WriteString(c.Text)
		}
	}

	flush()

	return items
}

// attributes returns the attributes EqualXML compares: namespace declarations aren't.
func attributes(e *xmldoc.Node) []xmldoc.Attr {
	return slices.DeleteFunc(slices.Clone(e.Attrs), func(a xmldoc.Attr) bool { return a.Decl })
}

func (o xmlOptions) sameElement(want, got *xmldoc.Node) bool {
	wantAttrs, gotAttrs := attributes(want), attributes(got)
	wantItems, gotItems := content(want), content(got)

	if !o.sameName(want.Name, got.Name) || len(wantAttrs) != len(gotAttrs) || len(wantItems) != len(gotItems) {
		return false
	}

	for _, wa := range wantAttrs {
		i := slices.IndexFunc(gotAttrs, func(ga xmldoc.Attr) bool { return o.sameName(wa.Name, ga.Name) })
		if i < 0 || gotAttrs[i].Value != wa.Value {
			return false
		}
	}

	if o.ignoreChildOrder {
		paired, _ := pairUp(len(wantItems), len(gotItems), func(w, g int) (bool, error) {
			return o.sameItem(wantItems[w], gotItems[g]), nil
		})

		return paired
	}

	for i := range wantItems {
		if !o.sameItem(wantItems[i], gotItems[i]) {
			return false
		}
	}

	return true
}

func (o xmlOptions) sameItem(want, got xmlItem) bool {
	if want.elem == nil || got.elem == nil {
		return want.elem == nil && got.elem == nil && want.text == got.text
	}

	return o.sameElement(want.elem, got.elem)
}

// Describe implements vlatte.SelfDescriptive. Options are described as an object after the expected XML, with the
// keys of mock files.
func (m *equalXMLMatcher) Describe() vlatte.Descriptor {
	opts := make(map[string]any, 2)
	if m.opts.ignoreChildOrder {
		opts["ignore_child_order"] = true
	}

	if m.opts.namespaces != XMLNamespacesByURI {
		opts["namespaces"] = m.opts.namespaces.String()
	}

	if len(opts) == 0 {
		return vlatte.DescribeSingleArg("eqx", m.Expected)
	}

	return vlatte.DescribeMultiArgs("eqx", m.Expected, opts)
}
