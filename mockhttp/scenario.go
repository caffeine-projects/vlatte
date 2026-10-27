package mockhttp

import (
	"context"
	"fmt"
	"iter"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Scenarios returns every scenario, by name: those the server's mocks name, with their states and the moves of their
// mocks, and those set without a mock.
func (app *Server) Scenarios(ctx context.Context) ([]matchscenario.Scenario, error) {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return nil, err
	}

	return app.scenarioStore.Model(uses), nil
}

// SetScenario moves the scenario name to state: the mocks that require state match next. It returns an error that
// wraps [matchscenario.ErrUnknownState] when no mock of the scenario names state, or state is empty. A scenario no
// mock names moves to any other state.
func (app *Server) SetScenario(ctx context.Context, name, state string) error {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return err
	}

	return app.scenarioStore.Move(uses, name, state)
}

// ResetScenario moves the scenario name back to [matchscenario.StateStarted].
func (app *Server) ResetScenario(name string) { app.scenarioStore.ResetScenario(name) }

// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
func (app *Server) ResetScenarios() { app.scenarioStore.ResetScenarios() }

// scenarioUses lists the scenario matchers of the server's mocks.
func (app *Server) scenarioUses(ctx context.Context) (iter.Seq[matchscenario.Use], error) {
	mocks, err := app.storage.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("http: listing mocks: %w", err)
	}

	return func(yield func(matchscenario.Use) bool) {
		for m := range mocks {
			for _, e := range m.spec.expectations {
				if sm, ok := e.Matcher.(*matchscenario.Matcher); ok && !yield(matchscenario.Use{MockID: m.ID(), Matcher: sm}) {
					return
				}
			}
		}
	}, nil
}
