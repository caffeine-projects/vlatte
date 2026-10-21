package match

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestContains(t *testing.T) {
	testCases := []struct {
		name     string
		expected any
		value    any
		result   bool
	}{
		{
			"should return true when value is contained in the matcher argument",
			"world",
			"hello world",
			true,
		},
		{
			"should return false when value is not contained in the matcher argument",
			"dev",
			"hello world",
			false,
		},
		{
			"should return true when items value is contained in the given slice",
			"dev",
			[]string{"dev", "qa"},
			true,
		},
		{
			"should return false when items value is not contained in the given slice",
			"po",
			[]string{"dev", "qa"},
			false,
		},
		{
			"should return true when items value is a key present in the given map",
			"dev",
			map[string]string{"dev": "ok", "qa": "nok"},
			true,
		},
		{
			"should return false when items value is a key not present in the given map",
			"unknown",
			map[string]string{"dev": "ok", "qa": "nok"},
			false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Contain(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			assert.Nil(t, err)
			assert.Equal(t, tc.result, ok)
		})
	}
}

func TestContainf(t *testing.T) {
	ok, err := Containf("%s", "qa").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "dev and qa"))
	assert.Nil(t, err)
	assert.True(t, ok)
}

func TestContain_ValuesItCantHold(t *testing.T) {
	// A value that holds nothing, or holds text when expected isn't text, doesn't match, and isn't an error:
	// an error would stop the search for a mock.
	for _, tc := range []struct {
		expected any
		value    any
	}{
		{"any", nil},
		{5, "a5b"},
		{"5", 5},
		{"a", true},
	} {
		ok, err := Contain(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
		require.NoError(t, err)
		require.False(t, ok, "%#v in %#v", tc.expected, tc.value)
	}
}

func TestContain_ReadsItemsAsEqualDoes(t *testing.T) {
	// Items and keys compare by Equal's rule: query values hold numbers, and no number holds the rune of its value.
	for _, tc := range []struct {
		expected any
		value    any
		want     bool
	}{
		{1, []string{"1", "2"}, true},
		{49, []string{"1"}, false},
		{1, []float64{1.9}, false},
		{1, map[string]any{"1": true}, true},
		{"a", []byte("cat"), true},
	} {
		ok, err := Contain(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
		require.NoError(t, err)
		require.Equal(t, tc.want, ok, "%#v in %#v", tc.expected, tc.value)
	}
}
