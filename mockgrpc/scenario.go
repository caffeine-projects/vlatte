package mockgrpc

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"time"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Scenarios returns every scenario, by name: those the server's mocks name, with their states and the moves of their
// mocks, and those set without a mock. A disabled mock counts, as it matches again once enabled.
func (app *Server) Scenarios(ctx context.Context) ([]matchscenario.Scenario, error) {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return nil, err
	}

	return app.scenarios.Model(uses), nil
}

// SetScenario moves the scenario name to state: the mocks that require state match next. It returns an error that
// wraps [matchscenario.ErrUnknownState] when no mock of the scenario names state, or state is empty. A scenario no
// mock names moves to any other state.
func (app *Server) SetScenario(ctx context.Context, name, state string) error {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return err
	}

	return app.scenarios.Move(uses, name, state)
}

// ResetScenario moves the scenario name back to [matchscenario.StateStarted]. A scenario no mock moved and none set is
// there already: the admin API's events get no move of it.
func (app *Server) ResetScenario(name string) {
	app.scenarios.ResetScenario(name)
}

// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
func (app *Server) ResetScenarios() {
	app.scenarios.ResetScenarios()
}

// scenarioUses lists the scenario matchers of the server's mocks, disabled ones too: a disabled mock matches again once
// enabled, so its scenario keeps the states it names.
func (app *Server) scenarioUses(ctx context.Context) (iter.Seq[matchscenario.Use], error) {
	mocks, err := app.storage.PickAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("grpc: listing mocks: %w", err)
	}

	var uses []matchscenario.Use
	for _, m := range mocks {
		for _, e := range m.expectations {
			if sm, ok := e.Matcher.(*matchscenario.Matcher); ok {
				uses = append(uses, matchscenario.Use{MockID: m.ID(), Matcher: sm})
			}
		}
	}

	return slices.Values(uses), nil
}

// movedScenarios returns the moves of the scenarios of mock, which Find picked, for the journal and the templates.
func movedScenarios(mock *Mock) []matchscenario.Move {
	var moves []matchscenario.Move

	for _, e := range mock.expectations {
		sm, ok := e.Matcher.(*matchscenario.Matcher)
		if !ok {
			continue
		}

		mv, ok := sm.Move()
		if !ok {
			continue
		}

		moves = append(moves, mv)
	}

	return moves
}

// publishScenario publishes c to the admin API's events. The scenario store calls it as it makes c, so the events
// stream in the order the scenarios changed, and a move a call made names it and its mock.
func (app *Server) publishScenario(c matchscenario.Change) {
	event := adminapi.Event{Type: adminapi.EventScenario, Time: time.Now(), Target: c.Scenario, Message: c.Message}
	if c.Claim != nil {
		if mock := c.Claim.Mock(); mock != nil {
			event.MockID = mock.ID()
		}

		if call := callOf(c.Claim.Context()); call != nil {
			event.RequestID = call.draft.ID()
		}
	}

	app.events.Publish(event)
}
