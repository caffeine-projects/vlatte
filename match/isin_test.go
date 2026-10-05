package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestIsIn(t *testing.T) {
	tcs := []struct {
		name     string
		items    []any
		value    any
		expected bool
	}{
		{"numbers", []any{10, 20, 30}, 20, true},
		{"string", []any{"10", "20", "30"}, "20", true},
		{"string -- number", []any{"10", "20", "30"}, 20, false},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, "test", true},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, "dev", false},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, true, true},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := IsIn(tc.items).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestInInWithInvalidItems(t *testing.T) {
	ok, err := IsIn(true).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), true))
	require.Error(t, err)
	require.False(t, ok)
}
