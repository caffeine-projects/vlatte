package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestHasSuffix(t *testing.T) {
	tcs := []struct {
		value    string
		expected bool
	}{
		{"world", true},
		{"hello", false},
	}

	for _, tc := range tcs {
		t.Run(tc.value, func(t *testing.T) {
			ok, err := HasSuffix(tc.value).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "hello world"))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
