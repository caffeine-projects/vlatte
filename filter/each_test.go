package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/require"
)

func TestEachSlice(t *testing.T) {
	s := []string{"test", "tested", "testing"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), s))

	require.NoError(t, err)
	require.True(t, ok)
}

func TestEachSliceNotPass(t *testing.T) {
	s := []string{"test", "tested", "dev"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), s))

	require.NoError(t, err)
	require.False(t, ok)
}

func TestEachMap(t *testing.T) {
	m := map[string]string{"1": "test", "2": "testing", "3": "tested"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), m))

	require.NoError(t, err)
	require.True(t, ok)
}

func TestEachMapNotPass(t *testing.T) {
	m := map[string]string{"1": "dev", "2": "testing", "3": "tested"}
	ok, err := Each(HasPrefix("test")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), m))

	require.NoError(t, err)
	require.False(t, ok)
}

func TestEachInvalidValueType(t *testing.T) {
	_, err := Each(Anything()).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "hello"))
	require.Error(t, err)
}
