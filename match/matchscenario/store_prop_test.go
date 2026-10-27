package matchscenario

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// _names spell two scenarios in several cases.
var _names = []string{"checkout", "Checkout", "CHECKOUT", "refund", "Refund"}

// _states are the states mocks require and move to; "" is no state.
var _states = []string{"", StateStarted, "paid", "refunded"}

// model is what the store must keep: the state of each scenario by its name in lower case, and the name it was first
// given.
type model struct {
	states map[string]string
	names  map[string]string
}

func (md *model) start(name string) {
	k := strings.ToLower(name)
	if _, ok := md.states[k]; !ok {
		md.states[k] = StateStarted
		md.names[k] = name
	}
}

func (md *model) state(name string) string {
	if state, ok := md.states[strings.ToLower(name)]; ok {
		return state
	}

	return StateStarted
}

func (md *model) scenarios() map[string]string {
	out := make(map[string]string, len(md.states))
	for k, state := range md.states {
		out[md.names[k]] = state
	}

	return out
}

func TestStore_KeepsStateAsAModelDoes(t *testing.T) {
	// Any sequence of matches, moves, sets and resets leaves the store where a plain map of states would be.
	rapid.Check(t, func(t *rapid.T) {
		store := New()
		md := &model{states: make(map[string]string), names: make(map[string]string)}

		t.Repeat(map[string]func(*rapid.T){
			"match": func(t *rapid.T) {
				name := rapid.SampledFrom(_names).Draw(t, "name")
				required := rapid.SampledFrom(_states).Draw(t, "required")

				ok, err := store.Matcher(name, required, "").Match(
					vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), nil))
				require.NoError(t, err)

				require.Equal(t, required == "" || md.state(name) == required, ok)
			},
			"claim": func(t *rapid.T) {
				name := rapid.SampledFrom(_names).Draw(t, "name")
				required := rapid.SampledFrom(_states).Draw(t, "required")
				next := rapid.SampledFrom(_states).Draw(t, "next")

				claimed := store.Matcher(name, required, next).Claim(vlatte.MatcherArg{})

				want := required == "" || md.state(name) == required
				require.Equal(t, want, claimed)

				if want && next != "" {
					md.start(name)
					md.states[strings.ToLower(name)] = next
				}
			},
			"set": func(t *rapid.T) {
				name := rapid.SampledFrom(_names).Draw(t, "name")
				state := rapid.SampledFrom(_states[1:]).Draw(t, "state")

				store.SetScenario(name, state)
				md.start(name)
				md.states[strings.ToLower(name)] = state
			},
			"reset one": func(t *rapid.T) {
				name := rapid.SampledFrom(_names).Draw(t, "name")

				store.ResetScenario(name)
				if _, ok := md.states[strings.ToLower(name)]; ok {
					md.states[strings.ToLower(name)] = StateStarted
				}
			},
			"reset": func(_ *rapid.T) {
				store.ResetScenarios()
				for k := range md.states {
					md.states[k] = StateStarted
				}
			},
			"": func(t *rapid.T) {
				require.Equal(t, md.scenarios(), store.Scenarios())
			},
		})
	})
}
