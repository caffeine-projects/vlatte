package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

var _json = map[string]any{
	"name": "someone",
	"age":  34,
	"address": map[string]any{
		"street": "very nice place",
	},
	"job": nil,
}

func TestJSONPath(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		matcher  vlatte.Matcher
		expected bool
	}{
		{
			name:     "should read match text field on object root",
			path:     "name",
			matcher:  StrictEqual("someone"),
			expected: true,
		},
		{
			name:     "should match numeric field value",
			path:     "age",
			matcher:  StrictEqual(34),
			expected: true,
		},
		{
			name:     "should match nested object field",
			path:     "address.street",
			matcher:  StrictEqual("very nice place"),
			expected: true,
		},
		{
			name:     "should match nil when field is present",
			path:     "job",
			matcher:  StrictEqual(nil),
			expected: true,
		},
		{
			name:     "not present matchers for absent field",
			path:     "address.city",
			matcher:  Not(Present()),
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := JSONPath(tc.path, tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), _json))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestJSONPath_ReadsBodiesAsDocumentsAre(t *testing.T) {
	// A body is read the way mock files are: a decimal is the float64 nearest to it, as in the file, and an integer
	// keeps every digit. ojg's own reader adds the fraction separately, which can differ in the last bit.
	for body, m := range map[string]vlatte.Matcher{
		`{"x": 0.3}`:                Equal(0.3),
		`{"x": 2.675}`:              Equal(2.675),
		`{"x": 9007199254740993}`:   Equal(int64(9007199254740993)),
		`{"x": 1.0}`:                StrictEqual(int64(1)),
		`{"x": "a", "x": "b"}`:      Equal("b"),
		"{\"x\": [1, 2], \"y\": 3}": Len(2),
	} {
		ok, err := JSONPath("$.x", m).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), body))
		require.NoError(t, err)
		require.True(t, ok, body)
	}

	ok, err := JSONPath("$.x", Equal(int64(1))).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []byte(`{"x": 1}`)))
	require.NoError(t, err)
	require.True(t, ok, "a body of another content type arrives as bytes")

	ok, err = JSONPath("$.x", Anything()).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "{"))
	require.NoError(t, err)
	require.False(t, ok, "a body that isn't JSON doesn't match")
}

func TestJSONPathMatcherMatchErrors(t *testing.T) {
	testCases := []struct {
		name    string
		path    string
		matcher vlatte.Matcher
	}{
		{name: "field not present", path: "life", matcher: StrictEqual("anything")},
		{name: "deep field not present", path: "path.to.another.field", matcher: StrictEqual("any")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := JSONPath(tc.path, tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), _json))
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
}

func TestJSONPathMatcherInvalidPaths(t *testing.T) {
	testCases := []struct {
		name    string
		path    string
		matcher vlatte.Matcher
	}{
		{name: "invalid path", path: "312nj.,", matcher: StrictEqual("anything")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Panics(t, func() {
				_, _ = JSONPath(tc.path, tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), _json))
			})
		})
	}
}

func TestJSONPathNew(t *testing.T) {
	require.Panics(t, func() {
		JSONPath(".", Eq(""))
	})

	require.Panics(t, func() {
		Field(".", Eq(""))
	})
}
