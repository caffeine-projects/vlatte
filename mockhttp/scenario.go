package mockhttp

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
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

	if err := app.scenarioStore.Move(uses, name, state); err != nil {
		return err
	}

	app.publishScenario(name, name+" set to "+state)

	return nil
}

// ResetScenario moves the scenario name back to [matchscenario.StateStarted].
func (app *Server) ResetScenario(name string) {
	app.scenarioStore.ResetScenario(name)
	app.publishScenario(name, name+" reset to "+matchscenario.StateStarted)
}

// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
func (app *Server) ResetScenarios() {
	app.scenarioStore.ResetScenarios()
	app.publishScenario("", "every scenario reset to "+matchscenario.StateStarted)
}

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

// movedScenarios keeps the moves of the scenarios of r's mock, which Find picked, for the journal, and publishes them to
// the admin API's events.
func (app *Server) movedScenarios(r *RequestValues) {
	for _, e := range r.Mock.spec.expectations {
		sm, ok := e.Matcher.(*matchscenario.Matcher)
		if !ok {
			continue
		}

		mv, ok := sm.Move()
		if !ok {
			continue
		}

		r.scenarioMoves = append(r.scenarioMoves, mv)

		if app.eventStream != nil {
			app.eventStream.Publish(adminapi.Event{
				Type:      adminapi.EventScenario,
				Time:      time.Now(),
				RequestID: r.ReqID,
				Target:    mv.Scenario,
				MockID:    r.Mock.ID(),
				Message:   mv.String(),
			})
		}
	}
}

// publishScenario publishes a move of the scenario target, or of every scenario when it is empty, which no request made,
// to the admin API's events.
func (app *Server) publishScenario(target, message string) {
	if app.eventStream != nil {
		app.eventStream.Publish(adminapi.Event{Type: adminapi.EventScenario, Time: time.Now(), Target: target, Message: message})
	}
}
