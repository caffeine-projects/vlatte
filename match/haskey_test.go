package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestHasKey(t *testing.T) {
	m := map[string]any{
		"name": "someone",
		"age":  34,
		"address": map[string]any{
			"street": "very nice place",
		},
		"options": []string{},
		"active":  false,
		"none":    nil,
		"zero":    0,
	}

	tcs := []struct {
		key      string
		expected bool
	}{
		{"name", true},
		{"age", true},
		{"address", true},
		{"address.street", true},
		{"address.city", false},
		{"active", true},
		{"zero", true},
		{"options", true},
		{"none", false},
	}

	for _, tc := range tcs {
		t.Run(tc.key, func(t *testing.T) {
			ok, err := HasKey(tc.key).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), m))
			require.Equal(t, tc.expected, ok)
			require.Nil(t, err)
		})
	}
}

func TestHasKeyNew(t *testing.T) {
	require.Panics(t, func() {
		HasKey(".")
	})
}

func TestHasKey_TextThatIsntJSON(t *testing.T) {
	ok, err := HasKey("$.a").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "{"))
	require.NoError(t, err)
	require.False(t, ok)
}
