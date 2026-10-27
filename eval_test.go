package vlatte_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestEval(t *testing.T) {
	makeFilter := func(name, label string) vlatte.Matcher {
		return match.Delegate(name, func(arg vlatte.MatcherArg) (bool, error) {
			arg.Explain().AddMessage(label + ": received value: " + fmt.Sprintf("%v", arg.Value()))
			return true, nil
		})
	}

	mat1 := match.All(makeFilter("test", "test01"), makeFilter("test", "test02"))

	ev1 := vlatte.Eval[any]{
		Key:      "key",
		Target:   0,
		Matcher:  mat1,
		Selector: func(ctx context.Context, input any) any { return "ok" },
	}
	ev2 := vlatte.Eval[any]{
		Key:      "key",
		Target:   0,
		Matcher:  makeFilter("test", "test03"),
		Selector: func(ctx context.Context, input any) any { return "ok" },
	}

	explainer := vlatte.NewExplanation()
	sub := explainer.Sub("matchers")

	evals := vlatte.Evaluators[*fakeMock, any]{&ev1, &ev2}
	evals.Eval(t.Context(), sub, nil)
}

// turnMatcher is the matcher of a mock that takes a turn, such as a scenario's: it matches while the turn is free,
// and another request takes the turn between its match and its claim when lose is set.
type turnMatcher struct {
	taken   bool
	lose    bool
	matches int

	// claimedWith is the value of the argument of its last claim.
	claimedWith any
}

func (m *turnMatcher) Name() string { return "turn" }

func (m *turnMatcher) Match(vlatte.MatcherArg) (bool, error) {
	m.matches++
	return !m.taken, nil
}

func (m *turnMatcher) Claim(arg vlatte.MatcherArg) bool {
	m.claimedWith = arg.Value()

	if m.taken || m.lose {
		m.taken = true
		return false
	}

	m.taken = true

	return true
}

func TestFind_TakesTheTurnOfTheMockFound(t *testing.T) {
	turn := &turnMatcher{}
	a := newFakeMockWith("a", 0)
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a}))
	evals := func(*fakeMock) vlatte.Evaluators[*fakeMock, request] {
		return vlatte.Evaluators[*fakeMock, request]{{Key: "turn", Matcher: turn}}
	}

	found, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), request{}, evals, nil)
	require.NoError(t, err)
	require.True(t, matched)
	require.Same(t, a, found)
	require.True(t, turn.taken)

	_, matched, err = seq.Find(t.Context(), vlatte.NewExplanation(), request{}, evals, nil)
	require.NoError(t, err)
	require.False(t, matched, "the turn is taken")
}

// A claim acts on what the request selects, such as the instance of a scenario its key names: it gets the value Match
// got.
func TestFind_AClaimGetsTheValueTheSelectorSelects(t *testing.T) {
	turn := &turnMatcher{}
	a := newFakeMockWith("a", 0)
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a}))
	evals := func(*fakeMock) vlatte.Evaluators[*fakeMock, request] {
		return vlatte.Evaluators[*fakeMock, request]{{
			Key:      "turn",
			Matcher:  turn,
			Selector: func(_ context.Context, in request) any { return in["path"] },
		}}
	}

	_, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), request{"path": "/t1"}, evals, nil)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, "/t1", turn.claimedWith)
}

func TestFind_LooksAgainWhenAnotherRequestTookTheTurn(t *testing.T) {
	// A request that matched a mock whose turn another request took since gets the mock that matches now, from the
	// first mock: the one that comes before may match in the state the other request left.
	in := request{"method": "GET"}
	turn := &turnMatcher{lose: true}
	var firstRuns int
	first, a := newFakeMockWith("first", 0), newFakeMockWith("a", 0)

	mocks := map[*fakeMock][]*vlatte.Eval[request]{
		first: {part("method", counted(match.Equal("GET"), &firstRuns)), {Key: "after", Matcher: vlatte.MatcherFunc("after",
			func(vlatte.MatcherArg) (bool, error) { return turn.taken, nil })}},
		a: {{Key: "turn", Matcher: turn}},
	}
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{first, a}))

	found, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), in,
		func(m *fakeMock) vlatte.Evaluators[*fakeMock, request] { return mocks[m] }, nil)
	require.NoError(t, err)
	require.True(t, matched)
	require.Same(t, first, found)
	require.Equal(t, 2, firstRuns, "Find looked again from the first mock")
	require.Equal(t, 1, turn.matches)
}

// brokenClaimer matches, but never takes its turn: it calls claimed, or panics, as it tries.
type brokenClaimer struct {
	claimed func()
	panics  bool
}

func (m brokenClaimer) Name() string                          { return "broken" }
func (m brokenClaimer) Match(vlatte.MatcherArg) (bool, error) { return true, nil }

func (m brokenClaimer) Claim(vlatte.MatcherArg) bool {
	if m.panics {
		panic("claim broke")
	}

	m.claimed()

	return false
}

func TestFind_AClaimerThatNeverTakesItsTurnEndsWithTheContext(t *testing.T) {
	// A Claimer whose Claim disagrees with its Match makes Find look again forever: the request's context still ends
	// it, so the request fails instead of hanging.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	claims := 0
	a := newFakeMockWith("a", 0)
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a}))
	evals := func(*fakeMock) vlatte.Evaluators[*fakeMock, request] {
		return vlatte.Evaluators[*fakeMock, request]{{Key: "broken", Matcher: brokenClaimer{claimed: func() {
			if claims++; claims == 3 {
				cancel()
			}
		}}}}
	}

	_, matched, err := seq.Find(ctx, vlatte.NewExplanation(), request{}, evals, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, matched)
	require.Equal(t, 3, claims, "Find looked again until the context ended")
}

func TestFind_AClaimThatPanicsIsAnError(t *testing.T) {
	// A panic in a Claimer fails the request, as one in a matcher does, instead of the server.
	a := newFakeMockWith("a", 0)
	seq := vlatte.Mocks[*fakeMock, request](slices.Values([]*fakeMock{a}))
	evals := func(*fakeMock) vlatte.Evaluators[*fakeMock, request] {
		return vlatte.Evaluators[*fakeMock, request]{{Key: "broken", Matcher: brokenClaimer{panics: true}}}
	}

	_, matched, err := seq.Find(t.Context(), vlatte.NewExplanation(), request{}, evals, nil)
	require.ErrorIs(t, err, vlatte.ErrMatchPanicked)
	require.ErrorIs(t, err, vlatte.ErrMatching)
	require.False(t, matched)
}
