package vlatte_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestExplanation_Nil(t *testing.T) {
	t.Run("records nothing", func(t *testing.T) {
		var exp *vlatte.Explanation

		sub := exp.Sub("x")
		sub.AddMessage("m")

		require.Nil(t, sub, "a nil Explanation must not hand out a block that keeps messages")
		require.Nil(t, sub.Sub("y"))
		require.True(t, sub.Root())
	})

	t.Run("evaluations share no state", func(t *testing.T) {
		// A user matcher that writes to its explanation, in evaluations run without one.
		// A block shared by those evaluations keeps their messages and races under -race.
		var explained atomic.Int64
		m := match.Delegate("explains", func(arg vlatte.MatcherArg) (bool, error) {
			arg.Explain().AddMessage("m")
			if arg.Explain() != nil {
				explained.Add(1)
			}

			return true, nil
		})
		evals := vlatte.Evaluators[*fakeMock, any]{{Matcher: m}}

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				for range 100 {
					ok, err := evals.Eval(t.Context(), nil, nil)
					assert.NoError(t, err)
					assert.True(t, ok)
				}
			})
		}
		wg.Wait()

		require.Zero(t, explained.Load(), "matchers run without an Explanation got a block to write to")
	})
}

func TestExplanation_Sub(t *testing.T) {
	root := vlatte.NewExplanation()
	child := root.Sub("child")
	child.AddMessage("why")

	require.True(t, root.Root())
	require.False(t, child.Root())
	require.Same(t, root, child.Parent)
	require.Len(t, root.Children, 1)
	require.Same(t, child, root.Children[0])
	require.Equal(t, []vlatte.ExplanationItem{{Message: "why"}}, child.Items)
	require.Empty(t, root.Items, "a message belongs to the block it was added to")
}
