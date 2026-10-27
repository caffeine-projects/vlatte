package matchscenario

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

func TestStore_SetMovesAScenarioMocksMatchNext(t *testing.T) {
	store := New()
	require.True(t, match(t, store.Matcher("checkout", StateStarted, "paid")))

	store.SetScenario("checkout", "paid")
	store.SetScenario("Refund", "asked")
	require.Equal(t, map[string]string{"checkout": "paid", "Refund": "asked"}, store.Scenarios())

	require.True(t, match(t, store.Matcher("checkout", "paid", "")), "a mock that requires the state set matches")
	require.False(t, match(t, store.Matcher("refund", "done", "")), "a scenario set before any mock started it has the state set")
}

func TestStore_SetKeepsTheNameAScenarioWasFirstGiven(t *testing.T) {
	// The admin API names a scenario in its path: any case moves it, and the list keeps the name of the mocks.
	store := New()
	store.SetScenario("Checkout", "paid")
	store.SetScenario("checkout", "refunded")

	require.Equal(t, map[string]string{"Checkout": "refunded"}, store.Scenarios())
}

func TestStore_ResetStartsEveryScenarioOver(t *testing.T) {
	store := New()
	pay := store.Matcher("checkout", StateStarted, "paid")
	require.True(t, match(t, pay))
	require.True(t, pay.Claim())
	store.SetScenario("Refund", "asked")

	store.ResetScenarios()

	require.Equal(t, map[string]string{"checkout": StateStarted, "Refund": StateStarted}, store.Scenarios())
	require.True(t, match(t, pay), "the scenario starts over")
}

func TestStore_ScenariosIsACopy(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid")

	states := store.Scenarios()
	states["checkout"] = "refunded"

	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios())
}

func TestStore_IsSafeForConcurrentUse(t *testing.T) {
	// Requests match and move scenarios on many goroutines while the admin API sets and resets them: the race
	// detector fails this test when the store isn't safe.
	store := New()

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			m := store.Matcher("checkout", StateStarted, "paid")
			m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
			m.Claim()
			store.SetScenario("Checkout", "refunded")
			store.Scenarios()
			store.ResetScenarios()
		})
	}
	wg.Wait()

	require.Len(t, store.Scenarios(), 1)
}

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

				if required == StateStarted {
					md.start(name)
				}

				state, started := md.states[strings.ToLower(name)]
				require.Equal(t, required == "" || !started || state == required, ok)
			},
			"claim": func(t *rapid.T) {
				name := rapid.SampledFrom(_names).Draw(t, "name")
				required := rapid.SampledFrom(_states).Draw(t, "required")
				next := rapid.SampledFrom(_states).Draw(t, "next")

				claimed := store.Matcher(name, required, next).Claim()

				if required == StateStarted {
					md.start(name)
				}

				state, started := md.states[strings.ToLower(name)]
				want := required == "" || !started || state == required
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
