package match

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

func requestURIMatches(t require.TestingT, expected string, v any) bool {
	ok, err := RequestURI(expected).Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

func TestRequestURI(t *testing.T) {
	// A recording must answer only the request it recorded: the exact path and query, so /items doesn't answer
	// /items?page=2. Query parameters are compared as a set, as clients may write them in any order.
	tests := []struct {
		name     string
		expected string
		value    string
		want     bool
	}{
		{name: "same path, no query", expected: "/items", value: "http://h/items", want: true},
		{name: "a query the expected has not", expected: "/items", value: "http://h/items?page=2", want: false},
		{name: "a query the request has not", expected: "/items?page=2", value: "http://h/items", want: false},
		{name: "same query", expected: "/items?page=2", value: "http://h/items?page=2", want: true},
		{name: "another value", expected: "/items?page=2", value: "http://h/items?page=3", want: false},
		{name: "an extra parameter", expected: "/items?page=2", value: "http://h/items?page=2&x=1", want: false},
		{name: "a missing parameter", expected: "/items?page=2&x=1", value: "http://h/items?page=2", want: false},
		{name: "parameters in another order", expected: "/items?a=1&b=2", value: "http://h/items?b=2&a=1", want: true},
		{name: "values of one name in another order", expected: "/items?a=1&a=2", value: "http://h/items?a=2&a=1", want: false},
		{name: "values of one name in order", expected: "/items?a=1&a=2", value: "http://h/items?a=1&a=2", want: true},
		{name: "an empty value and no value", expected: "/items?a=", value: "http://h/items?a", want: true},
		{name: "a lone ? and no query", expected: "/items?", value: "http://h/items", want: true},
		{name: "a space as + or %20", expected: "/items?q=a+b", value: "http://h/items?q=a%20b", want: true},
		{name: "a trailing slash", expected: "/items/", value: "http://h/items", want: false},
		{name: "an escaped slash is not a slash", expected: "/a%2Fb", value: "http://h/a/b", want: false},
		{name: "an escaped slash", expected: "/a%2Fb", value: "http://h/a%2Fb", want: true},
		{name: "the case of an escape", expected: "/a%2fb", value: "http://h/a%2Fb", want: true},
		{name: "a needless escape", expected: "/%41", value: "http://h/A", want: true},
		{name: "path case", expected: "/Items", value: "http://h/items", want: false},
		{name: "a literal *", expected: "/files/*", value: "http://h/files/a", want: false},
		{name: "a literal * matches itself", expected: "/files/*", value: "http://h/files/*", want: true},
		{name: "unicode", expected: "/caf%C3%A9", value: "http://h/café", want: true},
		{name: "a query with a semicolon compares as text", expected: "/x?a=1;b=2", value: "http://h/x?a=1;b=2", want: true},
		{name: "a query with a semicolon, in another order", expected: "/x?a=1;b=2", value: "http://h/x?b=2;a=1", want: false},
		{name: "the fragment of the request doesn't count", expected: "/x", value: "http://h/x#top", want: true},
		{name: "the fragment of expected counts", expected: "/x#top", value: "http://h/x#bottom", want: false},
		{name: "the same fragment", expected: "/x#top", value: "http://h/x#top", want: true},
		{name: "the scheme and host don't count", expected: "/x", value: "https://other:8443/x", want: true},
		{name: "a relative value", expected: "/x?a=1", value: "/x?a=1", want: true},
		{name: "a path that starts with //", expected: "//a/b", value: "http://h//a/b", want: true},
		{name: "a path that starts with // is not a host", expected: "//a/b", value: "http://h/b", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, requestURIMatches(t, tc.expected, tc.value))
		})
	}

	t.Run("a value that isn't a URL doesn't match", func(t *testing.T) {
		for _, v := range []any{nil, 10, "%zz", (*url.URL)(nil), []string{"/x"}} {
			require.False(t, requestURIMatches(t, "/x", v), "%#v", v)
		}
	})

	t.Run("URL values", func(t *testing.T) {
		u, err := url.Parse("http://h/x?a=1")
		require.NoError(t, err)

		require.True(t, requestURIMatches(t, "/x?a=1", u))
		require.True(t, requestURIMatches(t, "/x?a=1", *u))
	})

	t.Run("it panics when expected isn't a URL", func(t *testing.T) {
		require.Panics(t, func() { RequestURI("%zz") })
	})

	t.Run("it describes itself as mock files write it", func(t *testing.T) {
		desc, ok := descriptor.IsSelfDescriptive(RequestURI("/items?page=2"))
		require.True(t, ok)

		data, err := desc.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, `["requestURI", "/items?page=2"]`, string(data))
	})
}

func TestPropRequestURI_MatchesWhatItNames(t *testing.T) {
	// Any path and query match themselves, however the client orders the parameters; changing one value breaks it.
	rapid.Check(t, func(t *rapid.T) {
		segments := rapid.SliceOfN(rapid.StringMatching(`[a-zA-Z0-9 ._~%/-]{0,6}`), 1, 3).Draw(t, "segments")
		for i, s := range segments {
			segments[i] = url.PathEscape(s)
		}

		path := "/" + strings.Join(segments, "/")

		names := rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z]{1,4}`), 0, 4, func(s string) string { return s }).Draw(t, "names")
		query := url.Values{}
		for _, name := range names {
			query[name] = rapid.SliceOfN(rapid.StringMatching(`[a-z0-9 &=+]{0,4}`), 1, 2).Draw(t, name)
		}

		expected := path
		if len(query) > 0 {
			expected += "?" + query.Encode()
		}

		// The same parameters, in the reverse of the order Encode writes.
		var parts []string
		for _, name := range names {
			for _, v := range query[name] {
				parts = append(parts, url.QueryEscape(name)+"="+url.QueryEscape(v))
			}
		}

		reordered := path
		if len(parts) > 0 {
			reordered += "?" + strings.Join(parts, "&")
		}

		require.True(t, requestURIMatches(t, expected, "http://h"+expected), expected)
		require.True(t, requestURIMatches(t, expected, "http://h"+reordered), "%s vs %s", expected, reordered)

		if len(names) > 0 {
			changed := url.Values{}
			for k, v := range query {
				changed[k] = append([]string(nil), v...)
			}

			changed[names[0]][0] += "x"
			require.False(t, requestURIMatches(t, expected, "http://h"+path+"?"+changed.Encode()))
		}
	})
}

func FuzzRequestURI(f *testing.F) {
	// No expected URL and no value make it fail or panic: a value that isn't a URL is a mismatch.
	for _, seed := range [][2]string{
		{"/items?page=2", "http://h/items?page=2"},
		{"/a%2Fb", "/a/b"},
		{"/x?a=1;b=2", "%zz"},
		{"/x#f", "http://h/x#f"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, expected, value string) {
		if _, err := url.Parse(expected); err != nil {
			return
		}

		m := RequestURI(expected)

		_, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), value))
		require.NoError(t, err)

		// A URL matches what RequestURI reads of itself, as the target of a request: one that starts with "/". An
		// opaque URL, such as mailto:x, has none.
		u, err := url.Parse(value)
		if err == nil && u.Opaque == "" && u.Fragment == "" && u.RawFragment == "" && strings.HasPrefix(u.RequestURI(), "/") {
			same := RequestURI(u.RequestURI())
			ok, err := same.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), u))
			require.NoError(t, err)
			require.True(t, ok, "%q against %q", u.RequestURI(), value)
		}
	})
}
