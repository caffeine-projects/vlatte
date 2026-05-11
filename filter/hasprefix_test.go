package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
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
			ok, err := HasPrefix(tc.value).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "hello world"))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
