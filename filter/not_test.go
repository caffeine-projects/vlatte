package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/require"
)

func TestNot(t *testing.T) {
	tcs := []struct {
		name     string
		matcher  vlatte.Filter
		expected bool
	}{
		{"is not equal", StrictEqual("dev"), true},
		{"is equal", StrictEqual("test"), false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Not(tc.matcher).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
