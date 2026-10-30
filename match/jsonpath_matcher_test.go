package match_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestJSONPath_MultipleResultsReachTheMatcherAsAList(t *testing.T) {
	matcher := match.JSONPath("$[*].name", match.Equal([]any{"tea", "coffee"}))
	arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), `[{"name":"tea"},{"name":"coffee"}]`)
	ok, err := matcher.Match(arg)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = match.JSONPath("$[*].name", match.Equal([]any{"tea"})).Match(arg)
	require.NoError(t, err)
	require.False(t, ok)
}
