package match

import (
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
	want     *xmlElement
	opts     xmlOptions
}

// Name implements vlatte.Matcher.
func (m *equalXMLMatcher) Name() string { return "EqualXML" }

// Match implements vlatte.Matcher.
func (m *equalXMLMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if text, ok := value.Text(v); ok {
		got, err := parseXML([]byte(text), m.opts.namespaces)
		if err == nil && m.opts.sameElement(m.want, got) {
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

func (o xmlOptions) sameElement(want, got *xmlElement) bool {
	if !o.sameName(want.name, got.name) || len(want.attrs) != len(got.attrs) || len(want.children) != len(got.children) {
		return false
	}

	for _, wa := range want.attrs {
		found := false
		for _, ga := range got.attrs {
			if o.sameName(wa.name, ga.name) {
				found = wa.value == ga.value
				break
			}
		}

		if !found {
			return false
		}
	}

	if o.ignoreChildOrder {
		paired, _ := pairUp(len(want.children), len(got.children), func(w, g int) (bool, error) {
			return o.sameNode(want.children[w], got.children[g]), nil
		})

		return paired
	}

	for i := range want.children {
		if !o.sameNode(want.children[i], got.children[i]) {
			return false
		}
	}

	return true
}

func (o xmlOptions) sameNode(want, got xmlNode) bool {
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
