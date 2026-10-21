package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestFalsy(t *testing.T) {
	testCases := []struct {
		name     string
		value    any
		expected bool
	}{
		{"bool", true, false},
		{"bool (false)", false, true},
		{"string (bool)", "true", false},
		{"string (bool) (2)", "false", true},
		{"string (txt)", "t", false},
		{"int", 0, true},
		{"int (2)", 1, false},
		// Falsy used to match every JSON number, since int64 and float64 read as false.
		{"JSON integer 0", int64(0), true},
		{"JSON integer 1", int64(1), false},
		{"JSON number 2.5", 2.5, false},
		{"nil is neither true nor false", nil, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Falsy().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestFalsyInvalidSyntax(t *testing.T) {
	// A value ParseBool can't read is neither true nor false, and isn't an error.
	for _, v := range []any{"y", nil, int64(2), []any{}} {
		ok, err := Falsy().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}
