// Package scenariotest checks that a mock server serves scenarios as [github.com/caffeine-projects/vlatte/match/matchscenario]
// defines them. A mock server runs [Run] from its own tests.
package scenariotest

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Mock is a mock of a scenario. It answers the request of [Server.Call] with its ID.
type Mock struct {
	// ID is the ID of the mock, and what it answers with.
	ID string

	// Scenario is the name of the scenario of the mock.
	Scenario string

	// RequiredState is the state the scenario must be in for the mock to match. Empty matches in any state.
	RequiredState string

	// NewState is the state the scenario moves to when the mock is picked to answer. Empty leaves it where it is.
	NewState string
}

// Server is a mock server under test, as the server's test builds it: the scenario methods are the server's own.
type Server interface {
	// Mock adds mocks to the server, each answering the same request with its ID.
	Mock(t *testing.T, mocks ...Mock)

	// Call sends the request every mock answers, and returns the ID of the mock that answered, or "" when none did.
	Call(t *testing.T) string

	// Scenarios returns every scenario, as [matchscenario.Store.Model] does for the server's mocks.
	Scenarios(ctx context.Context) ([]matchscenario.Scenario, error)

	// SetScenario moves a scenario, as [matchscenario.Store.Move] does for the server's mocks.
	SetScenario(ctx context.Context, name, state string) error

	// ResetScenario moves a scenario back to [matchscenario.StateStarted].
	ResetScenario(name string)

	// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
	ResetScenarios()
}

// Run runs the suite: each test starts a server with newServer, which starts it with no mocks and stops it when t
// ends.
func Run(t *testing.T, newServer func(t *testing.T) Server) {
	t.Run("a scenario moves through its states as its mocks answer", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid", NewState: "refunded"},
			Mock{ID: "done", Scenario: "checkout", RequiredState: "refunded"},
		)

		require.Equal(t, "pay", srv.Call(t))
		require.Equal(t, "refund", srv.Call(t))
		require.Equal(t, "done", srv.Call(t))
		require.Equal(t, "done", srv.Call(t), "a mock with no new state leaves the scenario where it is")
		require.Equal(t, "refunded", stateOf(t, srv, "checkout"))
	})

	t.Run("a scenario nothing moved is in started", func(t *testing.T) {
		// A mock that requires another state waits for the scenario to get there: a typo in a scenario's name, or a
		// missing first mock, makes the mock never match rather than match every request.
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid"})

		require.Empty(t, srv.Call(t))
		require.Equal(t, matchscenario.StateStarted, stateOf(t, srv, "checkout"))
	})

	t.Run("a mock that requires no state matches in any state", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "any", Scenario: "checkout"},
			Mock{ID: "unused", Scenario: "checkout", RequiredState: "paid"},
		)

		require.Equal(t, "any", srv.Call(t))

		require.NoError(t, srv.SetScenario(t.Context(), "checkout", "paid"))
		require.Equal(t, "any", srv.Call(t), "a mock that requires no state comes first, as it was added first")
	})

	t.Run("a new state alone moves the scenario", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "pay", Scenario: "checkout", NewState: "paid"})

		require.Equal(t, "pay", srv.Call(t))
		require.Equal(t, "paid", stateOf(t, srv, "checkout"))
	})

	t.Run("a state set is the one mocks match next", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid"},
		)

		require.NoError(t, srv.SetScenario(t.Context(), "checkout", "paid"))

		require.Equal(t, "refund", srv.Call(t))
	})

	t.Run("a state no mock names is refused", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"})

		require.ErrorIs(t, srv.SetScenario(t.Context(), "checkout", "payed"), matchscenario.ErrUnknownState)
		require.Equal(t, "pay", srv.Call(t), "nothing moved")

		require.NoError(t, srv.SetScenario(t.Context(), "refund", "asked"), "a scenario no mock names takes any state")
		require.Equal(t, "asked", stateOf(t, srv, "refund"))
	})

	t.Run("the scenarios list the states and moves of the mocks", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "Checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "receipt", Scenario: "checkout", RequiredState: "refunded"},
		)

		got, err := srv.Scenarios(t.Context())
		require.NoError(t, err)
		require.Equal(t, []matchscenario.Scenario{{
			Name:  "Checkout",
			State: matchscenario.StateStarted,
			States: []matchscenario.State{
				{Name: matchscenario.StateStarted, Reachable: true},
				{Name: "paid", Reachable: true},
				{Name: "refunded", Reachable: false},
			},
			Transitions: []matchscenario.Transition{{From: matchscenario.StateStarted, To: "paid", MockID: "pay"}},
		}}, got)
	})

	t.Run("a reset starts scenarios over, one or all", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "refund", RequiredState: "asked"},
		)

		require.Equal(t, "pay", srv.Call(t))
		require.Empty(t, srv.Call(t), "no mock matches in paid, nor before refund is asked")
		require.NoError(t, srv.SetScenario(t.Context(), "refund", "asked"))

		srv.ResetScenario("CHECKOUT")
		require.Equal(t, matchscenario.StateStarted, stateOf(t, srv, "checkout"))
		require.Equal(t, "asked", stateOf(t, srv, "refund"), "a reset of one leaves the others")

		srv.ResetScenarios()
		require.Equal(t, matchscenario.StateStarted, stateOf(t, srv, "refund"))
		require.Equal(t, "pay", srv.Call(t))
	})

	t.Run("names ignore case", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "Checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid", NewState: "refunded"},
		)

		require.Equal(t, "pay", srv.Call(t))
		require.Equal(t, "refund", srv.Call(t))

		got, err := srv.Scenarios(t.Context())
		require.NoError(t, err)
		require.Len(t, got, 1, "one scenario")
		require.Equal(t, "Checkout", got[0].Name, "under the name its first mock gives it")
		require.Equal(t, "refunded", got[0].State)
	})

	t.Run("concurrent requests take one turn each", func(t *testing.T) {
		// Picking a mock and moving its scenario is one step: two requests in started never both get the mock of
		// started, as they would if the state moved only after the answer.
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid", NewState: "refunded"},
			Mock{ID: "done", Scenario: "checkout", RequiredState: "refunded"},
		)

		const calls = 30

		var mu sync.Mutex
		got := make(map[string]int)

		t.Run("calls", func(t *testing.T) {
			for i := range calls {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					t.Parallel()

					id := srv.Call(t)

					mu.Lock()
					got[id]++
					mu.Unlock()
				})
			}
		})

		require.Equal(t, map[string]int{"pay": 1, "refund": 1, "done": calls - 2}, got)
	})

	t.Run("concurrent requests share a mock that requires no state", func(t *testing.T) {
		// Only a mock that requires a state takes a turn. One that requires none matches in every state, so every
		// request gets it, as it moves the scenario again and again.
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "any", Scenario: "checkout", NewState: "paid"})

		const calls = 30

		var mu sync.Mutex
		got := make(map[string]int)

		t.Run("calls", func(t *testing.T) {
			for i := range calls {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					t.Parallel()

					id := srv.Call(t)

					mu.Lock()
					got[id]++
					mu.Unlock()
				})
			}
		})

		require.Equal(t, map[string]int{"any": calls}, got)
		require.Equal(t, "paid", stateOf(t, srv, "checkout"))
	})

	t.Run("servers keep their own scenarios", func(t *testing.T) {
		a, b := newServer(t), newServer(t)
		for _, srv := range []Server{a, b} {
			srv.Mock(t, Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"})
		}

		require.Equal(t, "pay", a.Call(t))
		require.Equal(t, "pay", b.Call(t), "a moved its own checkout only")
	})
}

// stateOf returns the state of the scenario name of srv, failing when srv lists no such scenario.
func stateOf(t *testing.T, srv Server, name string) string {
	t.Helper()

	scenarios, err := srv.Scenarios(t.Context())
	require.NoError(t, err)

	i := slices.IndexFunc(scenarios, func(scn matchscenario.Scenario) bool { return strings.EqualFold(scn.Name, name) })
	require.NotEqual(t, -1, i, "the server lists no scenario %s: %v", name, scenarios)

	return scenarios[i].State
}
