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

func TestExplanation_MessagesListsABlockThenEachOfItsBlocksDepthFirst(t *testing.T) {
	// A template can't walk the blocks, so Messages is how it lists every reason, in the order of the blocks.
	root := vlatte.NewExplanation()
	first := root.Sub("first")
	first.Sub("inner").AddMessage("inner")
	first.AddMessage("first")
	root.Sub("second").AddMessage("second")
	root.AddMessage("root")

	require.Equal(t, []string{"root", "first", "inner", "second"}, root.Messages())
	require.Nil(t, (*vlatte.Explanation)(nil).Messages())
	require.Nil(t, vlatte.NewExplanation().Messages(), "a template ranges over nothing when no matcher said why")
}

func TestExplanation_MessagesAllocatesOnce(t *testing.T) {
	// A template lists the messages on every request its mock answers: one slice, whatever the blocks.
	root := vlatte.NewExplanation()
	for _, name := range []string{"a", "b", "c"} {
		block := root.Sub(name)
		block.AddMessage(name)
		block.Sub("inner").AddMessage(name + " inner")
	}

	require.Equal(t, 1.0, testing.AllocsPerRun(100, func() { _ = root.Messages() }))
}

func BenchmarkExplanation_Messages(b *testing.B) {
	// A template lists the messages on every request its mock answers.
	root := vlatte.NewExplanation()
	for _, name := range []string{"a", "b", "c"} {
		block := root.Sub(name)
		block.AddMessage(name)
		block.Sub("inner").AddMessage(name + " inner")
	}

	b.ReportAllocs()

	for b.Loop() {
		_ = root.Messages()
	}
}
