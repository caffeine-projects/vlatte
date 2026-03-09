package mfeat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScenario(t *testing.T) {
	t.Run("should match when required state is started and scenario is created", func(t *testing.T) {
		store := NewScenarioStore()
		matcher := Scenario(store, "scenario-1", ScenarioStateStarted, "second")

		ok, _, err := matcher.Match(nil)
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("should match when state equals required state", func(t *testing.T) {
		store := NewScenarioStore()
		_, _, _ = Scenario(store, "s1", ScenarioStateStarted, "second").Match(nil)
		stateful, _ := Scenario(store, "s1", ScenarioStateStarted, "second").(interface{ SetState() error })
		_ = stateful.SetState()

		matcher := Scenario(store, "s1", "second", "")
		ok, _, err := matcher.Match(nil)
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}
