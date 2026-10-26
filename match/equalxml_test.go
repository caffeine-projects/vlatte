package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

func xmlMatches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

func TestEqualXML_ComparesWhatTheXMLMeans(t *testing.T) {
	m := EqualXML(`<order id="7" state="new"><item sku="a">2</item><note>hi there</note></order>`)

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{"same", `<order id="7" state="new"><item sku="a">2</item><note>hi there</note></order>`, true},
		{"bytes", []byte(`<order id="7" state="new"><item sku="a">2</item><note>hi there</note></order>`), true},
		{"indented", "<?xml version=\"1.0\"?>\n<order id=\"7\" state=\"new\">\n  <item sku=\"a\"> 2 </item>\n  <note>hi there</note>\n</order>\n", true},
		{"attributes in any order", `<order state="new" id="7"><item sku="a">2</item><note>hi there</note></order>`, true},
		{"comments and processing instructions", `<!-- c --><order id="7" state="new"><?pi x?><item sku="a">2</item><!-- c --><note>hi there</note></order>`, true},
		{"CDATA is text", `<order id="7" state="new"><item sku="a"><![CDATA[2]]></item><note>hi <![CDATA[there]]></note></order>`, true},
		{"another attribute value", `<order id="8" state="new"><item sku="a">2</item><note>hi there</note></order>`, false},
		{"an extra attribute", `<order id="7" state="new" x="1"><item sku="a">2</item><note>hi there</note></order>`, false},
		{"a missing attribute", `<order id="7"><item sku="a">2</item><note>hi there</note></order>`, false},
		{"other text", `<order id="7" state="new"><item sku="a">3</item><note>hi there</note></order>`, false},
		{"text inside is kept", `<order id="7" state="new"><item sku="a">2</item><note>hi  there</note></order>`, false},
		{"children in another order", `<order id="7" state="new"><note>hi there</note><item sku="a">2</item></order>`, false},
		{"an extra child", `<order id="7" state="new"><item sku="a">2</item><note>hi there</note><x/></order>`, false},
		{"another name", `<orders id="7" state="new"><item sku="a">2</item><note>hi there</note></orders>`, false},
		{"not XML", `<order id="7"`, false},
		{"mismatched tags", `<order id="7" state="new"><item sku="a">2</note><note>hi there</item></order>`, false},
		{"two roots", `<order id="7" state="new"><item sku="a">2</item><note>hi there</note></order><order/>`, false},
		{"not text", 7, false},
		{"nil", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, xmlMatches(t, m, tc.value))
		})
	}
}

func TestEqualXML_IgnoreChildOrder(t *testing.T) {
	m := EqualXML(`<body><entry>1</entry><entry>2</entry><total>3</total></body>`, IgnoreChildOrder())

	require.True(t, xmlMatches(t, m, `<body><total>3</total><entry>2</entry><entry>1</entry></body>`))
	require.False(t, xmlMatches(t, m, `<body><entry>2</entry><entry>2</entry><total>3</total></body>`), "each child its own")
	require.False(t, xmlMatches(t, m, `<body><entry>2</entry><entry>1</entry></body>`), "still every child")
	require.False(t, xmlMatches(t, EqualXML(`<body><entry>1</entry><entry>2</entry></body>`), `<body><entry>2</entry><entry>1</entry></body>`),
		"in order without the option")
}

func TestEqualXML_Namespaces(t *testing.T) {
	const expected = `<s:outer xmlns:s="http://stuff.example.com" xmlns:m="http://more.example.com"><m:inner m:id="1">111</m:inner></s:outer>`

	tests := []struct {
		name  string
		mode  XMLNamespaceMode
		value string
		want  bool
	}{
		{"uri: same", XMLNamespacesByURI, expected, true},
		{"uri: other prefixes", XMLNamespacesByURI, `<a:outer xmlns:a="http://stuff.example.com"><b:inner xmlns:b="http://more.example.com" b:id="1">111</b:inner></a:outer>`, true},
		{"uri: a default namespace", XMLNamespacesByURI, `<outer xmlns="http://stuff.example.com"><b:inner xmlns:b="http://more.example.com" b:id="1">111</b:inner></outer>`, true},
		{"uri: another URI", XMLNamespacesByURI, `<s:outer xmlns:s="http://other.example.com" xmlns:m="http://more.example.com"><m:inner m:id="1">111</m:inner></s:outer>`, false},
		{"uri: no namespace", XMLNamespacesByURI, `<outer><inner id="1">111</inner></outer>`, false},
		{"uri: an unbound prefix isn't XML", XMLNamespacesByURI, `<s:outer><m:inner m:id="1">111</m:inner></s:outer>`, false},

		{"strict: same", XMLNamespacesStrict, expected, true},
		{"strict: other prefixes", XMLNamespacesStrict, `<a:outer xmlns:a="http://stuff.example.com"><b:inner xmlns:b="http://more.example.com" b:id="1">111</b:inner></a:outer>`, false},

		{"none: same", XMLNamespacesIgnored, expected, true},
		{"none: other declarations are other attributes", XMLNamespacesIgnored, `<s:outer xmlns:s="http://other.example.com" xmlns:m="http://more.example.com"><m:inner m:id="1">111</m:inner></s:outer>`, false},
		{"none: other prefixes are other names", XMLNamespacesIgnored, `<a:outer xmlns:a="http://stuff.example.com" xmlns:m="http://more.example.com"><m:inner m:id="1">111</m:inner></a:outer>`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, xmlMatches(t, EqualXML(expected, XMLNamespaces(tc.mode)), tc.value))
		})
	}

	// Ignoring namespaces, a prefix needs no declaration.
	require.True(t, xmlMatches(t, EqualXML(`<s:a>1</s:a>`, XMLNamespaces(XMLNamespacesIgnored)), `<s:a>1</s:a>`))
}

func TestEqualXML_UnprefixedAttributesHaveNoNamespace(t *testing.T) {
	// A default namespace names elements, not attributes.
	m := EqualXML(`<a xmlns="urn:x" id="1"/>`)
	require.True(t, xmlMatches(t, m, `<p:a xmlns:p="urn:x" id="1"/>`))
	require.False(t, xmlMatches(t, m, `<p:a xmlns:p="urn:x" p:id="1"/>`))
}

func TestEqualXML_ReadsTheEncodingADocumentDeclares(t *testing.T) {
	body := append([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><name>Jos`), 0xe9, '<', '/', 'n', 'a', 'm', 'e', '>')
	require.True(t, xmlMatches(t, EqualXML(`<name>José</name>`), body))
}

func TestEqualXML_PanicsOnWhatIsntXML(t *testing.T) {
	for _, expected := range []string{"", "text", "<a>", "<a></b>", "<a/><b/>", `<p:a/>`} {
		err, ok := panicOf(func() { EqualXML(expected) }).(error)
		require.True(t, ok, expected)
		require.ErrorContains(t, err, "match: eqx: ", expected)
	}
}

func TestEqualXML_DescribesItsOptions(t *testing.T) {
	data, err := EqualXML(`<a/>`).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["eqx", "<a/>"]`, string(data))

	data, err = EqualXML(`<a/>`, IgnoreChildOrder(), XMLNamespaces(XMLNamespacesStrict)).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["eqx", "<a/>", {"ignore_child_order": true, "namespaces": "strict"}]`, string(data))
}

func TestEqualXML_OnlyXMLWhiteSpaceIsTrimmed(t *testing.T) {
	// A non-breaking space is text, not the white space between elements.
	require.False(t, xmlMatches(t, EqualXML("<a></a>"), "<a>&#160;</a>"))
	require.False(t, xmlMatches(t, EqualXML("<a>x</a>"), "<a>&#160;x</a>"))
	require.True(t, xmlMatches(t, EqualXML("<a>x</a>"), "<a>\t\r\n x \n</a>"))
	require.False(t, xmlMatches(t, EqualXML("<a/>"), "&#160;<a/>"), "text outside the root")
	require.True(t, xmlMatches(t, EqualXML("<a/>"), " \n<a/>\n"))
}

func TestEqualXML_NamespaceModesAreNamedAsMockFilesWriteThem(t *testing.T) {
	modes := map[XMLNamespaceMode]string{
		XMLNamespacesByURI: "uri", XMLNamespacesStrict: "strict", XMLNamespacesIgnored: "none",
		XMLNamespaceMode(99): "XMLNamespaceMode(99)",
	}
	for mode, want := range modes {
		require.Equal(t, want, mode.String())
	}
}

func TestEQX_IsEqualXML(t *testing.T) {
	require.True(t, xmlMatches(t, EQX(`<a><b/></a>`, IgnoreChildOrder()), `<a> <b/> </a>`))
	require.False(t, xmlMatches(t, EQX(`<a><b/></a>`), `<a><c/></a>`))
}

func TestEqualXML_TheXMLPrefixNeedsNoDeclaration(t *testing.T) {
	// xml is bound by XML itself; any other prefix must be declared, on an attribute as on an element.
	require.True(t, xmlMatches(t, EqualXML(`<a xml:lang="en"/>`), `<a xml:lang="en"/>`))
	require.False(t, xmlMatches(t, EqualXML(`<a xml:lang="en"/>`), `<a xml:lang="pt"/>`))
	require.False(t, xmlMatches(t, EqualXML(`<a x="1"/>`), `<a p:x="1"/>`), "an unbound prefix isn't XML")
}

func TestEqualXML_NamesItself(t *testing.T) {
	// A matcher's name is what logs and failures show.
	require.Equal(t, "EqualXML", EqualXML("<a/>").Name())
}
