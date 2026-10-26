package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

const _todoList = `<todo-list owner="ann">
	<todo-item id="1">wash the car</todo-item>
	<todo-item id="2">walk the dog</todo-item>
	<todo-item id="3">wash the dishes</todo-item>
</todo-list>`

func TestXPath_SelectsWhatTheInnerMatcherGets(t *testing.T) {
	tests := []struct {
		name string
		m    vlatte.Matcher
		want bool
	}{
		{"an element exists", XPath("/todo-list[count(todo-item) = 3]", Present()), true},
		{"no element", XPath("/todo-list[count(todo-item) = 4]", Present()), false},
		{"nothing selected is nil", XPath("/todo-list/done", Nil()), true},
		{"an attribute, as text", XPath("/todo-list/@owner", Equal("ann")), true},
		{"a text node", XPath("//todo-item[@id='2']/text()", Contain("dog")), true},
		{"many, as a list", XPath("//todo-item/text()", Len(3)), true},
		{"some of many", XPath("//todo-item/text()", Some(HasPrefix("walk"))), true},
		{"each of many", XPath("//todo-item/text()", Each(HasPrefix("wash"))), false},
		{"an element, as XML", XPath("//todo-item[@id='1']", EqualXML(`<todo-item id="1">wash the car</todo-item>`)), true},
		{"a number", XPath("count(//todo-item)", Equal(3)), true},
		{"a bool", XPath("count(//todo-item) > 2", Truthy()), true},
		{"a string", XPath("string(/todo-list/@owner)", Equal("ann")), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, xmlMatches(t, tc.m, _todoList))
			require.Equal(t, tc.want, xmlMatches(t, tc.m, []byte(_todoList)), "bytes")
		})
	}
}

func TestXPath_ValuesThatArentXML(t *testing.T) {
	m := XPath("/a", Anything())
	for _, v := range []any{"<a>", "text", 7, nil} {
		require.False(t, xmlMatches(t, m, v), "%v", v)
	}
}

func TestXPath_Namespaces(t *testing.T) {
	const doc = `<s:outer xmlns:s="http://stuff.example.com" xmlns:m="http://more.example.com"><m:inner>111</m:inner></s:outer>`

	// Names are compared as written, unless a prefix is bound; local-name() ignores the prefix.
	inner := EqualXML(`<m:inner xmlns:m="http://more.example.com">111</m:inner>`)
	require.True(t, xmlMatches(t, XPath("/s:outer/m:inner", inner), doc))
	require.False(t, xmlMatches(t, XPath("/outer/inner", Present()), doc))
	require.True(t, xmlMatches(t, XPath("/*[local-name()='outer']/*[local-name()='inner']", inner), doc))

	m := XPath("/stuff:outer/more:inner[.=111]", Present(),
		XPathNamespace("stuff", "http://stuff.example.com"), XPathNamespace("more", "http://more.example.com"))
	require.True(t, xmlMatches(t, m, doc))

	other := XPath("/stuff:outer", Present(), XPathNamespace("stuff", "http://other.example.com"))
	require.False(t, xmlMatches(t, other, doc))

	// A bound prefix selects by namespace, whatever prefix the document writes.
	renamed := `<a:outer xmlns:a="http://stuff.example.com"><a:inner>111</a:inner></a:outer>`
	require.True(t, xmlMatches(t, XPath("/stuff:outer", Present(), XPathNamespace("stuff", "http://stuff.example.com")), renamed))
}

func TestXPath_ReturnsTheErrorOfTheInnerMatcher(t *testing.T) {
	errBoom := errors.New("boom")

	_, err := XPath("/a", Function(func(any) (bool, error) { return false, errBoom })).
		Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "<a/>"))
	require.ErrorIs(t, err, errBoom)
}

func TestXPath_MatchesAtOnce(t *testing.T) {
	// Mocks match requests at once: one matcher serves them all.
	m := XPath("//todo-item/text()", Len(3))

	done := make(chan bool)
	for range 8 {
		go func() {
			ok, _ := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), _todoList))
			done <- ok
		}()
	}

	for range 8 {
		require.True(t, <-done)
	}
}

func TestXPath_PanicsOnWhatIsntAnExpression(t *testing.T) {
	err, ok := panicOf(func() { XPath("//[", Anything()) }).(error)
	require.True(t, ok)
	require.ErrorContains(t, err, "match: xpath: the expression //[ is invalid: ")
}

func TestXPath_Describes(t *testing.T) {
	data, err := XPath("/a", Equal("1")).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["xpath", "/a", ["eq", "1"]]`, string(data))

	data, err = XPath("/p:a", Present(), XPathNamespace("p", "urn:p")).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["xpath", "/p:a", ["present"], {"namespaces": {"p": "urn:p"}}]`, string(data))
}

func TestXPath_AnElementKeepsTheNamespacesInScope(t *testing.T) {
	// An element is given as its XML for EqualXML to compare: the namespaces its ancestors declare must come with it,
	// or its names lose their namespace, or their prefix isn't bound and it isn't XML at all.
	tests := []struct {
		name string
		doc  string
		m    vlatte.Matcher
	}{
		{"a default namespace", `<order xmlns="urn:o"><id>7</id></order>`, XPath("/*/*", EqualXML(`<id xmlns="urn:o">7</id>`))},
		{"a prefix", `<o:order xmlns:o="urn:o"><o:id>7</o:id></o:order>`, XPath("/o:order/o:id", EqualXML(`<x:id xmlns:x="urn:o">7</x:id>`))},
		{
			"the innermost declaration", `<o:order xmlns:o="urn:outer"><o:id xmlns:o="urn:o">7</o:id></o:order>`,
			XPath("//*[local-name()='id']", EqualXML(`<o:id xmlns:o="urn:o">7</o:id>`)),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, xmlMatches(t, tc.m, tc.doc))
		})
	}
}

func TestXPath_NamesAreAsWrittenWhateverTheOrderOfDeclarations(t *testing.T) {
	// Without XPathNamespace, a name selects by its prefix as written: two prefixes of one namespace, or a default
	// namespace declared before or after a prefix, must not change what an expression selects.
	tests := []struct {
		doc  string
		expr string
		want bool
	}{
		{`<order xmlns:o="urn:o" xmlns="urn:o" o:id="7"/>`, "/order/@o:id", true},
		{`<order xmlns:o="urn:o" xmlns="urn:o" o:id="7"/>`, "/order/@id", false},
		{`<order xmlns="urn:o" xmlns:o="urn:o" o:id="7"/>`, "/order/@o:id", true},
		{`<order xmlns="urn:o" xmlns:o="urn:o" o:id="7"/>`, "/order/@id", false},
		{`<root xmlns:a="urn:o" xmlns:b="urn:o"><a:child>1</a:child></root>`, "/root/a:child", true},
		{`<root xmlns:a="urn:o" xmlns:b="urn:o"><a:child>1</a:child></root>`, "/root/child", false},
		{`<root xmlns:a="urn:o" xmlns:b="urn:o"><a:child>1</a:child></root>`, "/root/b:child", false},
	}

	for _, tc := range tests {
		require.Equal(t, tc.want, xmlMatches(t, XPath(tc.expr, Present()), tc.doc), "%s on %s", tc.expr, tc.doc)
	}
}

func TestXPath_SelectsTheNodesOfTheDocument(t *testing.T) {
	// XPath 1.0's nodes: no declaration that the document doesn't have, a processing instruction isn't an element,
	// and text that is only white space is a text node.
	tests := []struct {
		name string
		doc  string
		m    vlatte.Matcher
	}{
		{"one node under the root", `<a/>`, XPath("count(/node())", Equal(1))},
		{"the root element first", `<a/>`, XPath("/node()", EqualXML("<a/>"))},
		{"a processing instruction isn't an element", `<?custom x?><a>1</a>`, XPath("/*", EqualXML("<a>1</a>"))},
		{"nor inside one", `<a><?pi?><b/></a>`, XPath("count(/a/*)", Equal(1))},
		{"white space is text", `<a> <b/> </a>`, XPath("count(/a/text())", Equal(2))},
		{"comments are nodes", `<a><!--c--><b/></a>`, XPath("count(/a/node())", Equal(2))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, xmlMatches(t, tc.m, tc.doc))
		})
	}
}

func TestXPath_AnElementIsGivenAsWritten(t *testing.T) {
	// Text matchers read an element's XML: its text keeps its line breaks and tabs, and an empty element its end tag.
	doc := "<r><a>line1\n\tline2 &amp; <![CDATA[<x>]]></a><e></e><f k=\"a&quot;b\"/></r>"

	require.True(t, xmlMatches(t, XPath("/r/a", Equal("<a>line1\n\tline2 &amp; &lt;x&gt;</a>")), doc))
	require.True(t, xmlMatches(t, XPath("/r/e", Equal("<e></e>")), doc))
	require.True(t, xmlMatches(t, XPath("/r/f", Equal(`<f k="a&#34;b"></f>`)), doc))
}

func TestXPath_ADocumentThatIsntWellFormedIsntXML(t *testing.T) {
	// The document is read as XML 1.0 with namespaces: one root, no text outside it, and each prefix bound.
	for _, doc := range []string{"<a/><b/>", "x<a/>", "<a/>x", `<a p:x="1"/>`} {
		require.False(t, xmlMatches(t, XPath("count(/*)", Anything()), doc), doc)
	}
}

func TestXPath_LastSkipsProcessingInstructions(t *testing.T) {
	require.True(t, xmlMatches(t, XPath("/r/node()[last()]", Equal("<b></b>")), "<r><b/><?pi?></r>"))
	require.True(t, xmlMatches(t, XPath("count(/r/b[1]/preceding-sibling::node())", Equal(0)), "<r><?pi?><b/><b/></r>"))
	require.True(t, xmlMatches(t, XPath("count(/r/node()[last()])", Equal(1)), "<r><?pi?><b/></r>"))

	// The document, and an attribute, have no siblings: last() of either is itself.
	require.True(t, xmlMatches(t, XPath("count(/self::node()[last()])", Equal(1)), "<r/>"))
	require.True(t, xmlMatches(t, XPath("count(/r/@a[last()])", Equal(1)), `<r a="1"/>`))
}

func TestXPath_NamesItself(t *testing.T) {
	require.Equal(t, "XPath", XPath("/a", Present()).Name())
}
