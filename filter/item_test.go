package filter

import (
	"strconv"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestItem(t *testing.T) {
	type model struct {
		name string
	}

	v := []any{"test", "dev", true, false, []string{"qa", "none"}, model{"go"}, 100}

	testCases := []struct {
		index    int
		matcher  vlatte.Filter
		expected bool
	}{
		{0, Equal("test"), true},
		{1, HasPrefix("d"), true},
		{1, Equal("test"), false},
		{2, Falsy(), false},
		{3, Falsy(), true},
		{4, Item(0, Equal("qa")), true},
		{5, Equal(model{"go"}), true},
		{6, GreaterThan(10), true},
		{6, LessThanOrEqual(10), false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(strconv.Itoa(tc.index), func(t *testing.T) {
			ok, err := Item(tc.index, tc.matcher).Match(vlatte.NewFilterArg(t.Context(), v))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestItemInvalidIndex(t *testing.T) {
	ok, err := Item(-1, Equal("dev")).Match(vlatte.NewFilterArg(t.Context(), []string{"dev"}))
	require.Error(t, err)
	require.False(t, ok)
}

func TestInvalidType(t *testing.T) {
	ok, err := Item(0, Equal("dev")).Match(vlatte.NewFilterArg(t.Context(), true))
	require.Error(t, err)
	require.False(t, ok)
}
