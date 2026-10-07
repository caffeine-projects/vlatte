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
	// A pattern that doesn't compile is a mistake in code, found when the matcher is made, not on each request.
	require.PanicsWithValue(t, "regexp: Compile(`[invalid`): error parsing regexp: missing closing ]: `[invalid`", func() { RegExpr("[invalid") })
}

func TestRegExpNonStringValue(t *testing.T) {
	// A value that isn't text doesn't match, and isn't an error.
	ok, err := RegExpr(".*").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), 42))
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = RegExpr("^4").Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []byte("42")))
	require.NoError(t, err)
	require.True(t, ok, "bytes are text")
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
