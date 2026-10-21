package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestLess(t *testing.T) {
	testCases := []struct {
		name     string
		expected float64
		value    any
		result   bool
	}{
		{"is less", 10, 5, true},
		{"is less fl", 9.9, 9.8, true},
		{"equal", 10, 10, false},
		{"greater", 5, 10, false},
		{"is less (string)", 10, "5", true},
		{"is less (float32)", 10, float32(5), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := LessThan(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.result, ok)
		})
	}
}

func TestLessUnhandledType(t *testing.T) {
	// A value that isn't a number doesn't match, and isn't an error: an error would stop the search for a mock.
	for _, v := range []any{true, nil, "ten", []any{11}} {
		ok, err := LessThan(10).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}
