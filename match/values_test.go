package match

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestTextMatchers_ValuesThatArentText(t *testing.T) {
	// A text matcher doesn't match a value that isn't text, and it isn't an error: an error would stop the search.
	for _, m := range []vlatte.Matcher{
		EqualIgnoreCase("a"), GlobMatch("*"), HasPrefix(""), HasSuffix(""), RegExpr(".*"), Trim(Anything()),
		ToLower(Anything()), ToUpper(Anything()), Split(",", Anything()), URLPath("*"), HasKey("$.a"),
	} {
		for _, v := range []any{5, nil, true, []any{"a"}, map[string]any{"a": "b"}} {
			ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
			require.NoError(t, err, "%s on %#v", m.Name(), v)

			if _, isObject := v.(map[string]any); m.Name() == "HasKey" && isObject {
				continue // a decoded object is JSON already
			}

			require.False(t, ok, "%s on %#v", m.Name(), v)
		}
	}
}

func TestHasKey_TextThatIsntJSON(t *testing.T) {
	ok, err := HasKey("$.a").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "{"))
	require.NoError(t, err)
	require.False(t, ok)
}

// stringer is a value whose String gives a URL, as some request types do.
type stringer string

func (s stringer) String() string { return string(s) }

func TestURLPath_TakesStringers(t *testing.T) {
	ok, err := URLPath("/a/*").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), stringer("http://x/a/b?c=d")))
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = URLPath("/a/*").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), url.URL{Path: "/a/b"}))
	require.NoError(t, err)
	require.True(t, ok)
}

func TestCombinators_PassOnErrorsOfUserCode(t *testing.T) {
	// Errors come only from user code now, and they still stop the search: they are bugs to fix.
	boom := Function(func(any) (bool, error) { return false, errors.New("boom") })

	for m, v := range map[vlatte.Matcher]any{
		Each(boom):          []any{1},
		Item(0, boom):       []any{1},
		Some(boom):          []any{1},
		URLPathMatch(boom):  "/a",
		Trim(boom):          "a",
		ToLower(boom):       "a",
		ToUpper(boom):       "a",
		Split(",", boom):    "a",
		JSONPath("$", boom): "{}",
	} {
		_, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.ErrorContains(t, err, "boom", m.Name())
	}
}
