package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestOrEqualLess(t *testing.T) {
	testCases := []struct {
		name     string
		expected float64
		value    any
		result   bool
	}{
		{"is less", 10, 5, true},
		{"is less fl", 9.9, 9.8, true},
		{"is equal", 10, 10, true},
		{"is equal fl", 9.9, 9.9, true},
		{"greater", 5, 10, false},
		{"greater (string)", 5, "10", false},
		{"greater (float32)", 5, float32(10), false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := LessThanOrEqual(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.result, ok)
		})
	}
}

func TestLessOrEqualUnhandledType(t *testing.T) {
	ok, err := LTE(10).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), true))

	require.Error(t, err)
	require.False(t, ok)
}
