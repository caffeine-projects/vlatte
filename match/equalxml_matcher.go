package match

import (
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

var (
	_ vlatte.Matcher             = (*equalXMLMatcher)(nil)
	_ descriptor.SelfDescriptive = (*equalXMLMatcher)(nil)
)

// equalXMLMatcher passes when the value is an XML document equal to the expected one.
type equalXMLMatcher struct {
	Expected string
	want     *xmlNode // the root element
	opts     xmlOptions
}

// Name implements vlatte.Matcher.
func (m *equalXMLMatcher) Name() string { return "EqualXML" }

// Match implements vlatte.Matcher.
func (m *equalXMLMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if text, ok := value.Text(v); ok {
		got, err := parseXML([]byte(text), m.opts.namespaces)
		if err == nil && m.opts.sameElement(m.want, got.root()) {
			return success()
		}
	}

	return mismatch(strings.Join([]string{"EqualXML(", m.Expected, ") Got: ", anyconv.Stringify(v)}, ""))
}

func (o xmlOptions) sameName(want, got xmlName) bool {
	switch o.namespaces {
	case XMLNamespacesStrict:
		return want == got
	default:
		return want.space == got.space && want.local == got.local
	}
}

// xmlItem is a part of an element's content, as EqualXML compares it: an element, or text.
type xmlItem struct {
	elem *xmlNode // nil for text
	text string
}

// content returns what EqualXML compares of an element's children: its elements, and the text between two of them,
// comments and processing instructions left out, trimmed of XML white space, when any is left.
func content(e *xmlNode) []xmlItem {
	var (
		items []xmlItem
		text  strings.Builder
	)

	flush := func() {
		if t := trimXMLSpace(text.String()); t != "" {
			items = append(items, xmlItem{text: t})
		}

		text.Reset()
	}

	for _, c := range e.children {
		switch c.kind {
		case _xmlElement:
			flush()

			items = append(items, xmlItem{elem: c})
		case _xmlText:
			text.WriteString(c.text)
		}
	}

	flush()

	return items
}

// attributes returns the attributes EqualXML compares: namespace declarations aren't.
func attributes(e *xmlNode) []xmlAttr {
	return slices.DeleteFunc(slices.Clone(e.attrs), func(a xmlAttr) bool { return a.decl })
}

func (o xmlOptions) sameElement(want, got *xmlNode) bool {
	wantAttrs, gotAttrs := attributes(want), attributes(got)
	wantItems, gotItems := content(want), content(got)

	if !o.sameName(want.name, got.name) || len(wantAttrs) != len(gotAttrs) || len(wantItems) != len(gotItems) {
		return false
	}

	for _, wa := range wantAttrs {
		i := slices.IndexFunc(gotAttrs, func(ga xmlAttr) bool { return o.sameName(wa.name, ga.name) })
		if i < 0 || gotAttrs[i].value != wa.value {
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

// Describe implements descriptor.SelfDescriptive. Options are described as an object after the expected XML, with the
// keys of mock files.
func (m *equalXMLMatcher) Describe() descriptor.Descriptor {
	opts := make(map[string]any, 2)
	if m.opts.ignoreChildOrder {
		opts["ignore_child_order"] = true
	}

	if m.opts.namespaces != XMLNamespacesByURI {
		opts["namespaces"] = m.opts.namespaces.String()
	}

	if len(opts) == 0 {
		return descriptor.SingleArg("eqx", m.Expected)
	}

	return descriptor.MultiArgs("eqx", m.Expected, opts)
}
