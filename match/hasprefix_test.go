package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestHasPrefix(t *testing.T) {
	tcs := []struct {
		value    string
		expected bool
	}{
		{"hello", true},
		{"world", false},
	}

	for _, tc := range tcs {
		t.Run(tc.value, func(t *testing.T) {
			ok, err := HasPrefix(tc.value).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "hello world"))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
