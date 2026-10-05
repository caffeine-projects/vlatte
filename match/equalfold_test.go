package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestEqualIgnoreCase(t *testing.T) {
	tcs := []struct {
		name              string
		expectedValue     string
		expectedValueArgs []any
		matchValue        any
		expected          bool
	}{
		{"nil value (empty)", "", nil, nil, true},
		{"nil value", "test", nil, nil, false},
		{"diff case", "TesT", nil, "test", true},
		{"equal values", "test", nil, "test", true},
		{"diff values", "TeST", nil, "TeST DEV", false},
		{"format diff case", "hello %s", []any{"WORLD"}, "hello world", true},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			var ok bool
			var err error

			if len(tc.expectedValueArgs) > 0 {
				ok, err = EqualIgnoreCasef(tc.expectedValue, tc.expectedValueArgs...).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.matchValue))
			} else {
				ok, err = EQI(tc.expectedValue).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.matchValue))
			}

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
