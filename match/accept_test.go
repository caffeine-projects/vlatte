package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestAccept_MatchesTheOfferTheClientPrefers(t *testing.T) {
	// A mock per representation: the one the client weighs highest must answer, whichever mock is tried first.
	offers := []string{"application/json", "application/xml"}

	tests := []struct {
		name   string
		accept any
		want   string
	}{
		{name: "no Accept header serves the first offer", accept: []string{}, want: "application/json"},
		{name: "a nil value serves the first offer", accept: nil, want: "application/json"},
		{name: "an Accept header that doesn't parse serves the first offer", accept: []string{";;;"}, want: "application/json"},
		{name: "one type", accept: []string{"application/xml"}, want: "application/xml"},
		{name: "q-values decide", accept: []string{"application/xml, application/json;q=0.1"}, want: "application/xml"},
		{name: "q-values over several headers", accept: []string{"application/json;q=0.2", "application/xml;q=0.9"}, want: "application/xml"},
		{name: "a tie goes to the order of the offers", accept: []string{"application/xml, application/json"}, want: "application/json"},
		{name: "a wildcard", accept: []string{"*/*"}, want: "application/json"},
		{name: "a more specific range beats a wildcard", accept: []string{"*/*;q=0.1, application/xml"}, want: "application/xml"},
		{name: "a refused type is passed over", accept: []string{"application/json;q=0, */*"}, want: "application/xml"},
		{name: "case and parameters don't count", accept: []string{"Application/XML; charset=utf-8"}, want: "application/xml"},
		{name: "the first value alone, as Header gives it", accept: "application/xml", want: "application/xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mediaType := range offers {
				ok, err := Accept(mediaType, offers...).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tt.accept))
				require.NoError(t, err)
				require.Equal(t, mediaType == tt.want, ok, "Accept(%s)", mediaType)
			}

			ok, err := NotAcceptable(offers...).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tt.accept))
			require.NoError(t, err)
			require.False(t, ok, "an offer is acceptable")
		})
	}
}

func TestNotAcceptable_MatchesWhenTheClientTakesNoOffer(t *testing.T) {
	// The 406 mock answers only a client that sent Accept and refused every offer; a client without one takes any.
	offers := []string{"application/json", "application/xml"}

	for _, accept := range [][]string{{"text/html"}, {"*/*;q=0"}, {"application/*;q=0, text/*"}} {
		arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), accept)

		ok, err := NotAcceptable(offers...).Match(arg)
		require.NoError(t, err)
		require.True(t, ok, "%v", accept)

		ok, err = Accept("application/json", offers...).Match(arg)
		require.NoError(t, err)
		require.False(t, ok, "%v", accept)
	}
}

func TestAccept_WithoutOffersTheMediaTypeIsTheOnlyOne(t *testing.T) {
	m := Accept("text/csv")

	for accept, want := range map[string]bool{"": true, "text/csv": true, "*/*": true, "application/json": false} {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), accept))
		require.NoError(t, err)
		require.Equal(t, want, ok, "Accept: %q", accept)
	}
}

func TestAccept_PanicsOnAMediaTypeItDoesNotOffer(t *testing.T) {
	// A typo in the media type would make a mock no client ever reaches.
	require.PanicsWithError(t, `match: accept: expected "application/josn" among the offers ["application/json" "application/xml"]`, func() {
		Accept("application/josn", "application/json", "application/xml")
	})
}

func TestNotAcceptable_WithoutOffersMatchesEveryAcceptThatParses(t *testing.T) {
	for accept, want := range map[string]bool{"": false, ";;;": false, "text/html": true} {
		ok, err := NotAcceptable().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), accept))
		require.NoError(t, err)
		require.Equal(t, want, ok, "Accept: %q", accept)
	}
}

func TestAccept_DescribesItselfAsItsMockFileSpec(t *testing.T) {
	// A mock the admin API lists, or a recording writes, reads back as the same matcher.
	data, err := Accept("application/xml", "application/json", "application/xml").(vlatte.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["accept", "application/xml", ["application/json", "application/xml"]]`, string(data))

	data, err = NotAcceptable("application/json").(vlatte.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["notacceptable", ["application/json"]]`, string(data))
}
