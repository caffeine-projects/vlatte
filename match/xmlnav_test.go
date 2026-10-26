package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

const _navDoc = `<a x="1"><!--note--><b id="1">one</b><?pi data?><b id="2">two<c>three</c></b>tail</a>`

func TestXMLNav_EveryAxisWalksTheDocument(t *testing.T) {
	// XPath walks the tree through the navigator: each axis below moves it another way.
	tests := []struct {
		expr string
		want vlatte.Matcher
	}{
		{"count(/a/b[2]/preceding-sibling::*)", Equal(1)},
		{"count(/a/b[1]/following-sibling::node())", Equal(2)},
		{"count(/a/b[2]/preceding-sibling::node())", Equal(2)},
		{"name(//@id[. = '2']/..)", Equal("b")},
		{"count(//@id/ancestor::*)", Equal(3)},
		{"count(//@id/node())", Equal(0)},
		{"count(//text()/@*)", Equal(0)},
		{"count(//comment())", Equal(1)},
		{"string(/a/comment())", Equal("note")},
		{"local-name(/a/comment())", Equal("")},
		{"string(/a/b[2])", Equal("twothree")},
		{"string(/)", Equal("onetwothreetail")},
		{"count(//@*)", Equal(3)},
		{"count(//c/ancestor-or-self::*)", Equal(3)},
		{"count(/a/b/c/preceding::b)", Equal(1)},
		{"count(/a/b[1]/following::*)", Equal(2)},
		{"name(/a/b[2]/@id/ancestor::*[1])", Equal("b")},
		{"count(/a/node()[1]/preceding-sibling::node())", Equal(0)},
		{"count(/*/..)", Equal(1)},
		{"count(/..)", Equal(0)},
		{"count(//b[/a/@x = '1'])", Equal(2)},
		{"/a", Equal(`<a x="1"><!--note--><b id="1">one</b><?pi data?><b id="2">two<c>three</c></b>tail</a>`)},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			require.True(t, xmlMatches(t, XPath(tc.expr, tc.want), _navDoc))
		})
	}
}

func TestXMLNav_MovesOnlyWithinItsDocument(t *testing.T) {
	a, err := parseXML([]byte("<a/>"), XMLNamespacesByURI)
	require.NoError(t, err)

	b, err := parseXML([]byte("<b/>"), XMLNamespacesByURI)
	require.NoError(t, err)

	nav := newXMLNav(a)
	require.False(t, nav.MoveTo(newXMLNav(b)))
	require.True(t, nav.MoveTo(newXMLNav(a)))
	require.False(t, nav.MoveToFirst(), "the document has no siblings")

	// The first sibling skips what XPath doesn't see, such as a processing instruction.
	c, err := parseXML([]byte("<a><?pi?><b/><c/></a>"), XMLNamespacesByURI)
	require.NoError(t, err)

	nav = newXMLNav(c)
	require.True(t, nav.MoveToChild())
	require.True(t, nav.MoveToChild())
	require.True(t, nav.MoveToNext())
	require.Equal(t, "c", nav.LocalName())
	require.True(t, nav.MoveToFirst())
	require.Equal(t, "b", nav.LocalName())
}
