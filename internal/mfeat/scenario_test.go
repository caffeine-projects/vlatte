package mfeat

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
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
