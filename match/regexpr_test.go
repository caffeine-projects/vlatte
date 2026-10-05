package match

import (
	"strconv"
	"testing"

	"github.com/caffeine-projects/vlatte"
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
			ok, err := RegExpr(tc.expression).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestRegExpInvalidExpression(t *testing.T) {
	ok, err := RegExpr("[invalid").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "anything"))
	require.Error(t, err)
	require.False(t, ok)
}

func TestRegExpNonStringValue(t *testing.T) {
	ok, err := RegExpr(".*").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), 42))
	require.Error(t, err)
	require.False(t, ok)
}

func TestRegExpConcurrent(t *testing.T) {
	m := RegExpr(`^\d+$`)
	const goroutines = 50

	results := make(chan bool, goroutines)
	for range goroutines {
		go func() {
			ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "123"))
			if err != nil {
				results <- false
				return
			}
			results <- ok
		}()
	}

	for range goroutines {
		require.True(t, <-results)
	}
}
