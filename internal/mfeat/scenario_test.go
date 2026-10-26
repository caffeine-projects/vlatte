package mfeat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestScenario(t *testing.T) {
	t.Run("should match when required state is started and scenario is created", func(t *testing.T) {
		store := NewScenarioStore()
		matcher := Scenario(store, "scenario-1", ScenarioStateStarted, "second")

		ok, err := matcher.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("should match when state equals required state", func(t *testing.T) {
		store := NewScenarioStore()
		first := Scenario(store, "s1", ScenarioStateStarted, "second")
		_, _ = first.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		_ = first.SetState()

		matcher := Scenario(store, "s1", "second", "")
		ok, err := matcher.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("should not match when state does not equal required state", func(t *testing.T) {
		store := NewScenarioStore()
		first := Scenario(store, "s1", ScenarioStateStarted, "second")
		_, _ = first.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		_ = first.SetState()

		matcher := Scenario(store, "s1", "third", "")
		ok, err := matcher.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("SetState with empty new state is no-op", func(t *testing.T) {
		store := NewScenarioStore()
		first := Scenario(store, "s1", ScenarioStateStarted, "second")
		_, _ = first.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		_ = first.SetState()

		noOp := Scenario(store, "s1", "second", "")
		_, _ = noOp.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		_ = noOp.SetState()

		// state should still be "second"
		matcher := Scenario(store, "s1", "second", "")
		ok, err := matcher.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("two stores are isolated", func(t *testing.T) {
		storeA := NewScenarioStore()
		storeB := NewScenarioStore()

		// Prime both stores with the same scenario name in their initial state.
		aInit := Scenario(storeA, "scenario", ScenarioStateStarted, "done")
		_, _ = aInit.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		_ = aInit.SetState() // storeA: "scenario" → "done"

		bInit := Scenario(storeB, "scenario", ScenarioStateStarted, "other")
		_, _ = bInit.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		// Do NOT call SetState on storeB — it stays in "started"

		// storeA transitioned to "done"; storeB is still "started"
		bMatch := Scenario(storeB, "scenario", "done", "")
		ok, err := bMatch.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.False(t, ok, "storeB scenario is still in 'started', should not match 'done'")

		aMatch := Scenario(storeA, "scenario", "done", "")
		ok, err = aMatch.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
		assert.NoError(t, err)
		assert.True(t, ok, "storeA scenario is in 'done', should match")
	})
}

func TestScenarioConcurrent(t *testing.T) {
	store := NewScenarioStore()
	const goroutines = 50

	// Prime the scenario so all goroutines can find it.
	primer := Scenario(store, "concurrent", ScenarioStateStarted, "next")
	_, _ = primer.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	_ = primer.SetState()

	errs := make(chan error, goroutines)
	for range goroutines {
		go func() {
			m := Scenario(store, "concurrent", "next", "next")
			_, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
			errs <- err
		}()
	}

	for range goroutines {
		assert.NoError(t, <-errs)
	}
}

func TestScenario_NamesAreExact(t *testing.T) {
	// A scenario is found under the name it was created with, whatever its case or spaces (#25): before, a name with
	// capitals or spaces was stored as written but looked up lowercased and trimmed, so its mocks matched every request
	// and its state never moved.
	for _, name := range []string{"checkout", "Checkout", " checkout "} {
		t.Run(name, func(t *testing.T) {
			store := NewScenarioStore()
			arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil)

			start := Scenario(store, name, ScenarioStateStarted, "paid")
			ok, err := start.Match(arg)
			assert.NoError(t, err)
			assert.True(t, ok)
			assert.NoError(t, start.SetState())

			ok, err = Scenario(store, name, ScenarioStateStarted, "paid").Match(arg)
			assert.NoError(t, err)
			assert.False(t, ok, "the scenario moved on to paid")

			ok, err = Scenario(store, name, "paid", "").Match(arg)
			assert.NoError(t, err)
			assert.True(t, ok)
		})
	}
}

func TestScenario_NamesDifferingInCaseAreTwoScenarios(t *testing.T) {
	// Names are exact, so mock files and the admin API, which names scenarios in paths, agree on them.
	store := NewScenarioStore()
	arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil)

	upper := Scenario(store, "Checkout", ScenarioStateStarted, "paid")
	_, _ = upper.Match(arg)
	assert.NoError(t, upper.SetState())

	ok, err := Scenario(store, "checkout", ScenarioStateStarted, "paid").Match(arg)
	assert.NoError(t, err)
	assert.True(t, ok, "checkout is still where it started")
}

func TestScenario_NoRequiredStateMatchesInAnyState(t *testing.T) {
	// A mock of a scenario that requires no state matches whatever state the scenario is in, as in WireMock. It
	// required "", the state of no scenario, so it stopped matching once its scenario existed.
	store := NewScenarioStore()
	arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil)
	anyState := Scenario(store, "checkout", "", "")

	ok, err := anyState.Match(arg)
	require.NoError(t, err)
	require.True(t, ok, "before the scenario starts")

	for _, state := range []string{ScenarioStateStarted, "paid"} {
		store.SetScenario("checkout", state)

		ok, err = anyState.Match(arg)
		require.NoError(t, err)
		require.True(t, ok, state)
	}
}

func TestScenario_ANewStateStartsTheScenario(t *testing.T) {
	// A mock's new state moves its scenario, one no mock started too, as in WireMock: it was dropped, so mocks that
	// require no state could never move their scenario.
	store := NewScenarioStore()
	move := Scenario(store, "checkout", "", "paid")

	ok, err := move.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, move.SetState())
	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestScenarioStore_SetAndReset(t *testing.T) {
	// The admin API moves scenarios: a state set is the one mocks match next, and a reset starts every scenario over.
	store := NewScenarioStore()
	arg := vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil)

	start := Scenario(store, "checkout", ScenarioStateStarted, "paid")
	_, _ = start.Match(arg)
	assert.Equal(t, map[string]string{"checkout": ScenarioStateStarted}, store.Scenarios())

	store.SetScenario("checkout", "paid")
	store.SetScenario("Refund", "asked")
	assert.Equal(t, map[string]string{"checkout": "paid", "Refund": "asked"}, store.Scenarios())

	ok, err := Scenario(store, "checkout", "paid", "").Match(arg)
	assert.NoError(t, err)
	assert.True(t, ok, "a mock that requires the state set matches")

	ok, err = Scenario(store, "Refund", "done", "").Match(arg)
	assert.NoError(t, err)
	assert.False(t, ok, "a scenario set before any mock started it has the state set")

	store.ResetScenarios()
	assert.Equal(t, map[string]string{"checkout": ScenarioStateStarted, "Refund": ScenarioStateStarted}, store.Scenarios())

	ok, err = start.Match(arg)
	assert.NoError(t, err)
	assert.True(t, ok, "the scenario starts over")
}
