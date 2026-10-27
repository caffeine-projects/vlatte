// Package scenariotest checks that a mock server serves scenarios as [github.com/caffeine-projects/vlatte/match/matchscenario]
// defines them. A mock server runs [Run] from its own tests.
package scenariotest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Mock is a mock of a scenario. It answers the request of [Server.Call] with its ID.
type Mock struct {
	// ID is what the mock answers with.
	ID string

	// Scenario is the name of the scenario of the mock.
	Scenario string

	// RequiredState is the state the scenario must be in for the mock to match. Empty matches in any state.
	RequiredState string

	// NewState is the state the scenario moves to once the mock answered. Empty leaves it where it is.
	NewState string
}

// Server is a mock server under test, as the server's test builds it.
type Server interface {
	// Mock adds mocks to the server, each answering the same request with its ID.
	Mock(t *testing.T, mocks ...Mock)

	// Call sends the request every mock answers, and returns the ID of the mock that answered, or "" when none did.
	Call(t *testing.T) string

	// Store is the store of the server's scenarios.
	Store() *matchscenario.Store
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
		require.Equal(t, map[string]string{"checkout": "refunded"}, srv.Store().Scenarios())
	})

	t.Run("a mock matches only in the state it requires", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid"})
		srv.Store().SetScenario("checkout", matchscenario.StateStarted)

		require.Empty(t, srv.Call(t))
	})

	t.Run("a mock that requires no state matches in any state", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "any", Scenario: "checkout"})

		require.Equal(t, "any", srv.Call(t), "before the scenario starts")

		for _, state := range []string{matchscenario.StateStarted, "paid"} {
			srv.Store().SetScenario("checkout", state)
			require.Equal(t, "any", srv.Call(t), state)
		}
	})

	t.Run("a new state alone starts the scenario", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "pay", Scenario: "checkout", NewState: "paid"})

		require.Equal(t, "pay", srv.Call(t))
		require.Equal(t, map[string]string{"checkout": "paid"}, srv.Store().Scenarios())
	})

	t.Run("a state set is the one mocks match next", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t,
			Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"},
			Mock{ID: "refund", Scenario: "checkout", RequiredState: "paid"},
		)

		srv.Store().SetScenario("checkout", "paid")

		require.Equal(t, "refund", srv.Call(t))
	})

	t.Run("a reset starts every scenario over", func(t *testing.T) {
		srv := newServer(t)
		srv.Mock(t, Mock{ID: "pay", Scenario: "checkout", RequiredState: matchscenario.StateStarted, NewState: "paid"})

		require.Equal(t, "pay", srv.Call(t))
		require.Empty(t, srv.Call(t), "no mock matches in paid")

		srv.Store().ResetScenarios()

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
		require.Equal(t, map[string]string{"Checkout": "refunded"}, srv.Store().Scenarios(), "one scenario, as first named")
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
