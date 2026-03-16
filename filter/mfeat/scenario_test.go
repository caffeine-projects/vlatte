package mfeat

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/assert"
)

func TestScenario(t *testing.T) {
	t.Run("should match when required state is started and scenario is created", func(t *testing.T) {
		store := NewScenarioStore()
		matcher := Scenario(store, "scenario-1", ScenarioStateStarted, "second")

		ok, err := matcher.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("should match when state equals required state", func(t *testing.T) {
		store := NewScenarioStore()
		first := Scenario(store, "s1", ScenarioStateStarted, "second")
		_, _ = first.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
		_ = first.SetState()

		matcher := Scenario(store, "s1", "second", "")
		ok, err := matcher.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}
