package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestNil(t *testing.T) {
	tcs := []struct {
		name     string
		value    any
		expected bool
	}{
		{"nil", nil, true},
		{"empty string", "", false},
		{"zero", 0, false},
		{"bool", false, false},
		{"text", "txt", false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Nil().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
