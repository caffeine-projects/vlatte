package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Truthy().Match(vlatte.NewFilterArg(t.Context(), explain.New(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestTruthyInvalidSyntax(t *testing.T) {
	ok, err := Truthy().Match(vlatte.NewFilterArg(t.Context(), explain.New(), "y"))

	require.Error(t, err)
	require.False(t, ok)
}
