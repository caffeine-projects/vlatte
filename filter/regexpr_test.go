package filter

import (
	"strconv"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/require"
)

func TestRegExpMatches(t *testing.T) {
	tcs := []struct {
		expression string
		value      any
		expected   bool
	}{
		{"tEsT", "tEsT", true},
		{"(?mi)hi", "/test?q=bye", false},
		{"(?mi)hi", "/test?q=hi", true},
	}

	for i, tc := range tcs {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			ok, err := RegExpr(tc.expression).Match(vlatte.NewFilterArg(t.Context(), explain.New(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}
