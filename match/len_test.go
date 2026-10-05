package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Len(tc.size).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
