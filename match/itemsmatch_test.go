package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestEqualElementsMatcher(t *testing.T) {
	testCases := []struct {
		name     string
		items    []any
		value    any
		expected bool
	}{
		{"arrays", []any{"a", "b", "c"}, []string{"c", "b", "a"}, true},
		{"arrays (more items)", []any{"a", "b", "c"}, []string{"c", "b", "a", "d", "e"}, false},
		{"arrays (more items)", []any{"c", "b", "a", "d", "e"}, []string{"a", "b", "c"}, false},
		{"arrays (repetitive)", []any{"a", "a", "b", "c"}, []string{"c", "b", "a", "a"}, true},
		{"arrays (repetitive)", []any{"a", "a", "b", "c"}, []string{"c", "b", "a", "a", "a"}, false},
		{"arrays (repetitive)", []any{"a", "a", "a", "b", "b", "c"}, []string{"c", "b", "a", "a", "a"}, false},
		{"query values read as numbers", []any{1, 2}, []string{"2", "1"}, true},
		{"no rune of a number", []any{49, 50}, []string{"1", "2"}, false},
		{"no truncation", []any{1.5}, []any{int64(1)}, false},
		// "1.0" and "1" both equal 1, but not each other: pairing "1.0" with 1 first must not hide the pairing that works.
		{"pairs up although equality isn't transitive", []any{1, "1.0"}, []any{"1.0", "1"}, true},
		{"not a list", []any{"a"}, "a", false},
		{"nil", []any{}, nil, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := ItemsMatch(tc.items).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
