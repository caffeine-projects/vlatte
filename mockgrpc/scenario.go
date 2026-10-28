package mockgrpc

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
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

// SetScenario moves the scenario name to state: its shared instance, or the one of a key in, such as
// matchscenario.Key("t1"). The mocks that require state match next. It returns an error that wraps
// [matchscenario.ErrUnknownState] when no mock of the scenario names state, or state is empty. A scenario no mock names
// moves to any other state.
func (app *Server) SetScenario(ctx context.Context, name, state string, in ...matchscenario.Instance) error {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return err
	}

	if err := app.scenarios.Move(uses, name, state, in...); err != nil {
		return err
	}

	app.publishScenario(name, matchscenario.Label(name, in...)+" set to "+state)

	return nil
}

// ResetScenario moves the scenario name back to [matchscenario.StateStarted]: every instance of it, or only the one of
// a key in, such as matchscenario.Key("t1"). An instance nothing moved and none set is there already: the admin API's
// events get no move of it.
func (app *Server) ResetScenario(name string, in ...matchscenario.Instance) {
	if app.scenarios.ResetScenario(name, in...) {
		app.publishScenario(name, matchscenario.Label(name, in...)+" reset to "+matchscenario.StateStarted)
	}
}

// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
func (app *Server) ResetScenarios() {
	app.scenarios.ResetScenarios()
	app.publishScenario("", "every scenario reset to "+matchscenario.StateStarted)
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

// scenarioKey returns the selector of the key of the instance of a scenario a call plays in: tmpl rendered with the
// data of reply templates.
func (app *Server) scenarioKey(tmpl string) (vlatte.Selector[*Call], error) {
	compiled, err := app.templateEngine().Compile("scenario.key", tmpl)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, call *Call) any {
		var key strings.Builder
		if err := compiled.Execute(&key, app.templateData(ctx, call)); err != nil {
			return err
		}

		return key.String()
	}, nil
}

// movedScenarios returns the moves of the scenarios of mock, which Find picked for call, of ctx, and publishes them to
// the admin API's events.
func (app *Server) movedScenarios(ctx context.Context, mock *Mock, call *Call) []matchscenario.Move {
	var moves []matchscenario.Move

	for _, e := range mock.expectations {
		sm, ok := e.Matcher.(*matchscenario.Matcher)
		if !ok {
			continue
		}

		mv, ok := sm.Move(e.Arg(ctx, nil, call))
		if !ok {
			continue
		}

		moves = append(moves, mv)

		if app.events != nil {
			event := adminapi.Event{
				Type: adminapi.EventScenario, Time: time.Now(), Target: mv.Scenario, MockID: mock.ID(),
				Message: mv.String(),
			}
			if call := callOf(ctx); call != nil {
				event.RequestID = call.draft.ID()
			}

			app.events.Publish(event)
		}
	}

	return moves
}

// publishScenario publishes a move of the scenario target, or of every scenario when it is empty, which no request made,
// to the admin API's events.
func (app *Server) publishScenario(target, message string) {
	if app.events != nil {
		app.events.Publish(adminapi.Event{Type: adminapi.EventScenario, Time: time.Now(), Target: target, Message: message})
	}
}
