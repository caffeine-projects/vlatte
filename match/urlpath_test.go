package match

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestURLPath(t *testing.T) {
	u, _ := url.Parse("http://localhost:8080/test/hello")

	testCases := []struct {
		name     string
		path     string
		value    any
		expected bool
	}{
		{"should accept a pointer", "/test/hello", u, true},
		{"should accept a string", "/test/hello", *u, true},
		{"should return false when it doesn't match", "/test/bye", u.String(), false},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := URLPath(tt.path).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tt.value))

			assert.Nil(t, err)
			assert.Equal(t, tt.expected, ok)

			ok, err = URLPathMatch(Contain(tt.path)).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tt.value))

			assert.Nil(t, err)
			assert.Equal(t, tt.expected, ok)
		})
	}

	t.Run("a value that isn't a URL doesn't match", func(t *testing.T) {
		for _, v := range []any{10, nil, "%zz"} {
			ok, err := URLPath("/test/hello").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
			require.NoError(t, err)
			require.False(t, ok)
		}
	})
}
