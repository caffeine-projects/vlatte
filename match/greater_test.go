package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestGreater(t *testing.T) {
	testCases := []struct {
		name     string
		expected float64
		value    any
		result   bool
	}{
		{"is less", 10, 5, false},
		{"is less fl", 9.9, 9.8, false},
		{"equal", 10, 10, false},
		{"greater", 5, 10, true},
		{"greater fl", 5.5, 6.5, true},
		{"greater fl (string)", 5.5, "6.5", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := GreaterThan(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.result, ok)
		})
	}
}

func TestGreaterUnhandledType(t *testing.T) {
	ok, err := GT(10).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), true))

	require.Error(t, err)
	require.False(t, ok)
}
