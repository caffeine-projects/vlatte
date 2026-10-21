package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestEachSlice(t *testing.T) {
	s := []string{"test", "tested", "testing"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), s))

	require.NoError(t, err)
	require.True(t, ok)
}

func TestEachSliceNotPass(t *testing.T) {
	s := []string{"test", "tested", "dev"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), s))

	require.NoError(t, err)
	require.False(t, ok)
}

func TestEachMap(t *testing.T) {
	m := map[string]string{"1": "test", "2": "testing", "3": "tested"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), m))

	require.NoError(t, err)
	require.True(t, ok)
}

func TestEachMapNotPass(t *testing.T) {
	m := map[string]string{"1": "dev", "2": "testing", "3": "tested"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), m))

	require.NoError(t, err)
	require.False(t, ok)
}

func TestEach_ValuesThatArentListsOrObjects(t *testing.T) {
	// A value Each can't take doesn't match, and isn't an error: an error would stop the search for a mock.
	for _, v := range []any{"hello", nil, 5} {
		ok, err := Each(Anything()).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}
