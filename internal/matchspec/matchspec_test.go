package matchspec

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

func matches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

func TestMatcherOf_TextIsEqualTextAndAListIsASpec(t *testing.T) {
	r := matchbuild.MustNew(matchbuild.WithMatcher("short", func(*matchbuild.Registry, []any) (vlatte.Matcher, error) {
		return match.Len(1), nil
	}))

	m, err := MatcherOf(r, config.Value{Key: "body", Any: "[short]"})
	require.NoError(t, err)
	require.True(t, matches(t, m, "[short]"), "text is compared as is")

	m, err = MatcherOf(r, config.Value{Key: "body", Any: []any{"short"}})
	require.NoError(t, err)
	require.True(t, matches(t, m, "a"), "a list names a matcher of the registry")
	require.False(t, matches(t, m, "ab"))
}

func TestMatcherOf_Problems(t *testing.T) {
	r := matchbuild.MustNew()

	for _, v := range []any{[]any{}, int64(1)} {
		_, err := MatcherOf(r, config.Value{Key: "body", Any: v})

		_, isProblem := errors.AsType[*config.Error](err)
		require.True(t, isProblem, "%v: %v", v, err)
	}

	_, err := MatcherOf(r, config.Value{Key: "body", Any: []any{"nope"}})
	require.EqualError(t, err, `unknown matcher "nope"`)
}
