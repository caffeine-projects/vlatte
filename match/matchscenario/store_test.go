package matchscenario

import (
	"context"
	"slices"
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
	require.False(t, match(t, store.Matcher("refund", "done", "")), "a scenario set before any mock moved it has the state set")
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
	require.True(t, pay.Claim(shared(t)))
	store.SetScenario("Refund", "asked")

	store.ResetScenarios()

	require.Equal(t, map[string]string{"checkout": StateStarted, "Refund": StateStarted}, store.Scenarios())
	require.True(t, match(t, pay), "the scenario starts over")
}

func TestStore_ResetOneStartsOnlyThatScenarioOver(t *testing.T) {
	store := New()
	store.SetScenario("Checkout", "paid")
	store.SetScenario("refund", "asked")

	store.ResetScenario("checkout")
	store.ResetScenario("nothing")

	require.Equal(t, map[string]string{"Checkout": StateStarted, "refund": "asked"}, store.Scenarios())
	require.Equal(t, StateStarted, store.State("nothing"))
}

func TestStore_NotifiesEachChangeAsItMakesIt(t *testing.T) {
	// A server streams the changes of its scenarios as the store makes them, so they stream in the order they were
	// made: a move a request made has the argument of its claim, which tells the request and its mock.
	store := New()

	var changes []Change
	store.Notify(func(c Change) { changes = append(changes, c) })

	arg := shared(t)
	store.Matcher("checkout", StateStarted, "paid").Claim(arg)
	store.Matcher("checkout", "paid", "").Claim(arg)
	store.SetScenario("refund", "asked")
	store.ResetScenario("checkout")
	store.ResetScenario("nothing")
	store.ResetScenarios()

	require.Len(t, changes, 4, "a claim that moves nothing and a reset of a scenario the store doesn't hold change nothing")
	require.NotNil(t, changes[0].Claim)
	require.Equal(t, t.Context(), changes[0].Claim.Context(), "the move has the argument of its claim")
	changes[0].Claim = nil

	require.Equal(t, []Change{
		{Scenario: "checkout", Message: "checkout moved from started to paid"},
		{Scenario: "refund", Message: "refund set to asked"},
		{Scenario: "checkout", Message: "checkout reset to started"},
		{Message: "every scenario reset to started"},
	}, changes)
}

func TestStore_NotifiesTheChangesOfAKey(t *testing.T) {
	// The board redraws the instance of the key a change names; a key that never moved doesn't change.
	store := New()

	var changes []string
	store.Notify(func(c Change) { changes = append(changes, c.Message) })

	store.Matcher("checkout", StateStarted, "paid").Claim(keyed(t, "t1"))
	store.SetScenario("checkout", "refunded", Key("t2"))
	store.ResetScenario("checkout", Key("t1"))
	store.ResetScenario("checkout", Key("t3"))
	store.ResetScenario("checkout", Key(""))

	require.Equal(t, []string{
		"checkout (key t1) moved from started to paid",
		"checkout (key t2) set to refunded",
		"checkout (key t1) reset to started",
		"checkout reset to started",
	}, changes)
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
			m.Claim(shared(t))
			m.Claim(keyed(t, "t1"))
			store.SetScenario("Checkout", "refunded")
			store.SetScenario("checkout", "paid", Key("t2"))
			store.Model(slices.Values([]Use{{MockID: "pay", Matcher: m}}))
			store.ResetScenario("checkout", Key("t1"))
			store.Scenarios()
			store.ResetScenarios()
		})
	}
	wg.Wait()

	require.Len(t, store.Scenarios(), 1)
}

func TestView_ReadsTheStateOfAScenario(t *testing.T) {
	// Templates read a scenario's state through a view, whatever the case of its name.
	store := New()
	view := store.View()

	require.Equal(t, StateStarted, view.State("checkout"))

	store.SetScenario("Checkout", "paid")
	require.Equal(t, "paid", view.State("CHECKOUT"))
}

func TestStore_ResetOfAKeyStartsOnlyThatInstanceOver(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid")
	store.SetScenario("checkout", "paid", Key("t1"))
	store.SetScenario("checkout", "refunded", Key("t2"))

	store.ResetScenario("CHECKOUT", Key("t1"))
	require.Equal(t, StateStarted, store.State("checkout", Key("t1")))
	require.Equal(t, "refunded", store.State("checkout", Key("t2")))
	require.Equal(t, "paid", store.State("checkout"))

	store.ResetScenario("checkout", Key(""))
	require.Equal(t, StateStarted, store.State("checkout"), "the empty key is the shared instance")
	require.Equal(t, "refunded", store.State("checkout", Key("t2")))
}

func TestStore_ResetOfAScenarioStartsEveryInstanceOver(t *testing.T) {
	// The board's "Start over" and DELETE /scenarios/{name} start a scenario over, whatever keys moved it.
	store := New()
	store.SetScenario("checkout", "paid")
	store.SetScenario("checkout", "paid", Key("t1"))
	store.SetScenario("refund", "asked", Key("t1"))

	store.ResetScenario("checkout")
	require.Equal(t, StateStarted, store.State("checkout"))
	require.Equal(t, StateStarted, store.State("checkout", Key("t1")))
	require.Equal(t, "asked", store.State("refund", Key("t1")), "a reset of one scenario leaves the others")

	store.ResetScenarios()
	require.Equal(t, StateStarted, store.State("refund", Key("t1")))
}

func TestStore_ScenariosListsTheSharedInstances(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid", Key("t1"))
	require.Empty(t, store.Scenarios(), "the shared instance didn't move")

	store.SetScenario("checkout", "refunded")
	require.Equal(t, map[string]string{"checkout": "refunded"}, store.Scenarios())
}

func TestView_ReadsTheStateOfAKey(t *testing.T) {
	// A template reads the instance of its request's key: {{ .Scenarios.StateOf "checkout" (.Request.Header.Get "X-Test") }}.
	store := New()
	view := store.View()
	store.SetScenario("checkout", "paid", Key("t1"))

	require.Equal(t, "paid", view.StateOf("Checkout", "t1"))
	require.Equal(t, StateStarted, view.StateOf("checkout", "t2"))
	require.Equal(t, StateStarted, view.StateOf("checkout", ""))
}

func TestLabel_NamesTheInstance(t *testing.T) {
	require.Equal(t, "checkout", Label("checkout"))
	require.Equal(t, "checkout", Label("checkout", Key("")))
	require.Equal(t, "checkout (key t1)", Label("checkout", Key("t1")))
}

func TestView_ReadsTheStateItsOwnMoveTookAScenarioTo(t *testing.T) {
	// A request's templates read the state its mock moved a scenario to, as its journal entry says, even once another
	// request moved the scenario on; a scenario it didn't move reads as it is.
	store := New()
	store.SetScenario("checkout", "paid")
	view := store.View(Move{Scenario: "checkout", From: StateStarted, To: "paid"})

	store.SetScenario("checkout", "shipped")
	store.SetScenario("refund", "asked")

	require.Equal(t, "paid", view.State("Checkout"))
	require.Equal(t, "asked", view.State("refund"))

	keyed := store.View(Move{Scenario: "checkout", Key: "t1", From: StateStarted, To: "paid"})
	store.SetScenario("checkout", "shipped", Key("t1"))

	require.Equal(t, "paid", keyed.StateOf("checkout", "t1"))
	require.Equal(t, "shipped", keyed.State("checkout"), "the move of a key leaves the shared instance as it is")
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
