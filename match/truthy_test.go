package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestTruthy(t *testing.T) {
	testCases := []struct {
		name     string
		value    any
		expected bool
	}{
		{"bool", true, true},
		{"bool (false)", false, false},
		{"string (bool)", "true", true},
		{"string (bool) (2)", "false", false},
		{"string (txt)", "t", true},
		{"int", 0, false},
		{"int (2)", 1, true},
		// JSON bodies give int64 and float64: Truthy used to read only Go ints, so a JSON 1 was never true.
		{"JSON integer 1", int64(1), true},
		{"JSON number 1.0", 1.0, true},
		{"number other than 0 or 1", int64(2), false},
		{"nil", nil, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Truthy().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestTruthyInvalidSyntax(t *testing.T) {
	// A value ParseBool can't read is neither true nor false, and isn't an error.
	for _, v := range []any{"y", nil, int64(2), []any{}} {
		ok, err := Truthy().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}
