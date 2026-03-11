package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
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
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Falsy().Match(vlatte.NewFilterArg(t.Context(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestFalsyInvalidSyntax(t *testing.T) {
	ok, err := Truthy().Match(vlatte.NewFilterArg(t.Context(), "y"))

	require.Error(t, err)
	require.False(t, ok)
}
