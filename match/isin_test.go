package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestIsIn(t *testing.T) {
	tcs := []struct {
		name     string
		items    []any
		value    any
		expected bool
	}{
		{"numbers", []any{10, 20, 30}, 20, true},
		{"string", []any{"10", "20", "30"}, "20", true},
		{"text items read as numbers, as Equal reads them", []any{"10", "20", "30"}, 20, true},
		{"a query value reads as a number", []any{1, 2}, "2", true},
		{"text that isn't a JSON number", []any{1, 2}, "02", false},
		{"no truncation", []any{1, 2}, 1.5, false},
		{"no rune of a number", []any{49}, "1", false},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, "test", true},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, "dev", false},
		{"mixed", []any{"city", 100, true, false, 2000, "all", "test"}, true, true},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := IsIn(tc.items).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
