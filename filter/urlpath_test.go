package filter

import (
	"net/url"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			ok, err := URLPath(tt.path).Match(vlatte.NewFilterArg(t.Context(), explain.New(), tt.value))

			assert.Nil(t, err)
			assert.Equal(t, tt.expected, ok)

			ok, err = URLPathMatch(Contain(tt.path)).Match(vlatte.NewFilterArg(t.Context(), explain.New(), tt.value))

			assert.Nil(t, err)
			assert.Equal(t, tt.expected, ok)
		})
	}

	t.Run("should return error when providing a type that is not handled by URLPath", func(t *testing.T) {
		ok, err := URLPath("/test/hello").Match(vlatte.NewFilterArg(t.Context(), explain.New(), 10))
		require.Error(t, err)
		require.False(t, ok)
	})
}
