package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Contain(tc.expected).Match(vlatte.NewFilterArg(t.Context(), tc.value))
			assert.Nil(t, err)
			assert.Equal(t, tc.result, ok)
		})
	}
}

func TestContainf(t *testing.T) {
	ok, err := Containf("%s", "qa").Match(vlatte.NewFilterArg(t.Context(), "dev and qa"))
	assert.Nil(t, err)
	assert.True(t, ok)
}

func TestContainNilValue(t *testing.T) {
	_, err := Contain("any").Match(vlatte.NewFilterArg(t.Context(), nil))
	require.Error(t, err)
}
