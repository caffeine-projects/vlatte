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
