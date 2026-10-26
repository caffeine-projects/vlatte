package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
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

func TestItemsMatch_ItemsThatAreMatchers(t *testing.T) {
	// WireMock's hasExactly: as many values as matchers, each matched by a matcher of its own, in any order.
	m := ItemsMatch([]vlatte.Matcher{Equal("1"), Contain("2"), Not(Contain("3"))})

	tests := []struct {
		value    any
		expected bool
	}{
		{[]string{"x", "1", "20"}, true},
		{[]string{"20", "x", "1"}, true},
		{[]string{"1", "20"}, false},
		{[]string{"1", "20", "x", "y"}, false},
		{[]string{"1", "20", "30"}, false},
		// "12" fits both Contain("2") and Not(Contain("3")), and "x" only the latter: pairing "12" with the latter
		// first must not hide the pairing that works.
		{[]string{"x", "1", "12"}, true},
		// Not(Contain("3")) fits "1" and "2", but each is another matcher's own.
		{[]string{"1", "2", "3"}, false},
	}

	for _, tc := range tests {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
		require.NoError(t, err)
		require.Equal(t, tc.expected, ok, "%v", tc.value)
	}
}

func TestItemsMatch_MixesMatchersAndValues(t *testing.T) {
	m := ItemsMatch([]any{HasPrefix("a"), 2})

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []any{"2", "ab"}))
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []any{"3", "ab"}))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestItemsMatch_ReturnsTheErrorOfAMatcher(t *testing.T) {
	errBoom := errors.New("boom")
	m := ItemsMatch([]vlatte.Matcher{Function(func(any) (bool, error) { return false, errBoom })})

	_, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []any{"a"}))
	require.ErrorIs(t, err, errBoom)
}

func TestItemsMatch_DescribesMatcherItemsAsHasExactly(t *testing.T) {
	// A mock file can't tell a list item that is a value from one that is a matcher spec, so items that are matchers
	// describe as hasExactly, every item a spec.
	data, err := ItemsMatch([]any{HasPrefix("a"), 2}).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["hasExactly", [["hasPrefix", "a"], ["eq", 2]]]`, string(data))

	data, err = ItemsMatch([]any{"a", 2}).(descriptor.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["itemsMatch", ["a", 2]]`, string(data))
}
