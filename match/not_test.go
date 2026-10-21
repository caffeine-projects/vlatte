package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestNot(t *testing.T) {
	tcs := []struct {
		name     string
		matcher  vlatte.Matcher
		expected bool
	}{
		{"is not equal", StrictEqual("dev"), true},
		{"is equal", StrictEqual("test"), false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Not(tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
