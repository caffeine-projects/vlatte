package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestLen_NegativeLengthPanics(t *testing.T) {
	// A negative length is a mistake in code; mock files catch it when they load.
	require.PanicsWithError(t, "match: len: the length must be 0 or more, not -1", func() { Len(-1) })
}

func TestLen_String(t *testing.T) {
	testCases := []struct {
		name     string
		value    any
		size     int
		expected bool
	}{
		{"string", "hello world -  ", 15, true},
		{"array", []string{"hi", "bye"}, 2, true},
		{"array (no match)", []string{"hi", "bye"}, 1, false},
		{"object", map[string]any{"a": 1}, 1, true},
		{"bytes", []byte("abc"), 3, true},
		// Len used to panic on these: a value without a length doesn't match.
		{"number", int64(3), 3, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Len(tc.size).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
