package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestSome(t *testing.T) {
	tcs := []struct {
		name     string
		matcher  vlatte.Matcher
		items    []any
		expected bool
	}{
		{"numbers", Equal(10), []any{10, 20, 30}, true},
		{"string", Contain("30"), []any{"10", "20", "30"}, true},
		{"string -- number", Equal(40), []any{"10", "20", "30"}, false},
		{"mixed", Equal("city"), []any{"city", 100, true, false, 2000, "all", "test"}, true},
		{"mixed", Equal("none"), []any{"city", 100, true, false, 2000, "all", "test"}, false},
		{"mixed", Equal(100), []any{"city", 100, true, false, 2000, "all", "test"}, true},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Some(tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.items))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestSomeErr(t *testing.T) {
	// An error of user code in the inner matcher still stops the search, as it is a bug to fix.
	ok, err := Some(Function(func(_ any) (bool, error) {
		return false, errors.New("boom")
	})).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []any{true}))

	require.EqualError(t, err, "some: fn: matcher: boom")
	require.False(t, ok)
}

func TestSome_ValuesThatArentLists(t *testing.T) {
	// A value that isn't a list doesn't match, and isn't an error.
	for _, v := range []any{true, nil, "abc", map[string]any{"a": 1}} {
		ok, err := Some(Anything()).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}
