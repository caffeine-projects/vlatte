package matchscenario

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// uses lists the matchers of mocks for Model and Move, each under the ID that goes with it.
func uses(pairs ...any) func(yield func(Use) bool) {
	return func(yield func(Use) bool) {
		for i := 0; i < len(pairs); i += 2 {
			if !yield(Use{MockID: pairs[i].(string), Matcher: pairs[i+1].(*Matcher)}) {
				return
			}
		}
	}
}

func TestStore_ModelListsTheStatesAndMovesOfEachScenario(t *testing.T) {
	// The admin page draws each scenario from its model: where it is, the states its mocks name, and which mock moves
	// it where.
	store := New()
	got := store.Model(uses(
		"pay", store.Matcher("Checkout", StateStarted, "paid"),
		"refund", store.Matcher("checkout", "paid", "refunded"),
		"receipt", store.Matcher("checkout", "refunded", ""),
		"cancel", store.Matcher("checkout", "", "cancelled"),
	))

	require.Equal(t, []Scenario{{
		Name:  "Checkout",
		State: StateStarted,
		States: []State{
			{Name: StateStarted, Reachable: true},
			{Name: "cancelled", Reachable: true},
			{Name: "paid", Reachable: true},
			{Name: "refunded", Reachable: true},
		},
		Transitions: []Transition{
			{From: StateStarted, To: "paid", MockID: "pay"},
			{From: "paid", To: "refunded", MockID: "refund"},
			{From: "", To: "cancelled", MockID: "cancel"},
		},
	}}, got)
}

func TestStore_ModelFlagsAStateNoMoveLeadsTo(t *testing.T) {
	// A mock that requires a state no mock moves to, from started, answers only once a test sets it: a likely typo.
	store := New()
	got := store.Model(uses(
		"refund", store.Matcher("checkout", "payed", "refunded"),
		"receipt", store.Matcher("checkout", "refunded", ""),
	))

	require.Equal(t, []State{
		{Name: StateStarted, Reachable: true},
		{Name: "payed", Reachable: false},
		{Name: "refunded", Reachable: false},
	}, got[0].States)
}

func TestStore_ModelHasTheStateAndScenariosThatMovedWithoutAMock(t *testing.T) {
	store := New()
	store.SetScenario("checkout", "paid")
	store.SetScenario("Legacy", "on")

	got := store.Model(uses("refund", store.Matcher("CHECKOUT", "paid", "")))

	require.Equal(t, []Scenario{
		{
			Name:   "CHECKOUT",
			State:  "paid",
			States: []State{{Name: StateStarted, Reachable: true}, {Name: "paid", Reachable: false}},
		},
		{Name: "Legacy", State: "on"},
	}, got, "in the order of their names, whatever their case; a scenario takes the name of its mocks")
}

func TestStore_MoveRefusesAStateNoMockNames(t *testing.T) {
	// A state set through the admin API that no mock requires leaves every mock of the scenario unmatched: the mistake
	// is caught when made, and the error lists the states there are.
	store := New()
	mocks := uses(
		"pay", store.Matcher("checkout", StateStarted, "paid"),
		"refund", store.Matcher("checkout", "paid", ""),
	)

	err := store.Move(mocks, "Checkout", "payed")
	require.ErrorIs(t, err, ErrUnknownState)
	require.EqualError(t, err, `scenario: unknown state: checkout has no state "payed"; its states are paid, started`)
	require.Empty(t, store.Scenarios(), "nothing moved")

	require.NoError(t, store.Move(mocks, "CHECKOUT", "paid"))
	require.Equal(t, map[string]string{"checkout": "paid"}, store.Scenarios(), "under the name of its mocks")
}

func TestStore_MoveSetsAnyStateOfAScenarioNoMockNames(t *testing.T) {
	// A test may set a scenario before it loads its mocks.
	store := New()

	require.NoError(t, store.Move(uses("pay", store.Matcher("checkout", StateStarted, "paid")), "refund", "asked"))
	require.Equal(t, map[string]string{"refund": "asked"}, store.Scenarios())
}

func TestStore_MoveRefusesNoState(t *testing.T) {
	// A mock that requires no state names none: a scenario moved to "" would match no mock, yet look as if it started.
	store := New()
	mocks := uses("any", store.Matcher("checkout", "", ""))

	for _, name := range []string{"checkout", "refund"} {
		require.ErrorIs(t, store.Move(mocks, name, ""), ErrUnknownState, name)
	}

	require.Empty(t, store.Scenarios(), "nothing moved")
}
