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
	require.True(t, xmlMatches(t, XPath("/s:outer/m:inner", Equal("<m:inner>111</m:inner>")), doc))
	require.False(t, xmlMatches(t, XPath("/outer/inner", Present()), doc))
	require.True(t, xmlMatches(t, XPath("/*[local-name()='outer']/*[local-name()='inner']", Equal("<m:inner>111</m:inner>")), doc))

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
