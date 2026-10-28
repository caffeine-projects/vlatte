package mockhttp

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
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

// Scenarios returns every scenario, by name: those the server's mocks name, with their states and the moves of their
// mocks, and those set without a mock. A disabled mock counts, as it matches again once enabled.
func (app *Server) Scenarios(ctx context.Context) ([]matchscenario.Scenario, error) {
	uses, err := app.scenarioUses(ctx)
	if err != nil {
		return nil, err
	}

	return app.scenarioStore.Model(uses), nil
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

	if err := app.scenarioStore.Move(uses, name, state, in...); err != nil {
		return err
	}

	app.publishScenario(name, matchscenario.Label(name, in...)+" set to "+state)

	return nil
}

// ResetScenario moves the scenario name back to [matchscenario.StateStarted]: every instance of it, or only the one of
// a key in, such as matchscenario.Key("t1").
func (app *Server) ResetScenario(name string, in ...matchscenario.Instance) {
	app.scenarioStore.ResetScenario(name, in...)
	app.publishScenario(name, matchscenario.Label(name, in...)+" reset to "+matchscenario.StateStarted)
}

// ResetScenarios moves every scenario back to [matchscenario.StateStarted].
func (app *Server) ResetScenarios() {
	app.scenarioStore.ResetScenarios()
	app.publishScenario("", "every scenario reset to "+matchscenario.StateStarted)
}

// scenarioUses lists the scenario matchers of the server's mocks, disabled ones too: a disabled mock matches again once
// enabled, so its scenario keeps the states it names.
func (app *Server) scenarioUses(ctx context.Context) (iter.Seq[matchscenario.Use], error) {
	mocks, err := app.storage.PickAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("http: listing mocks: %w", err)
	}

	var uses []matchscenario.Use
	for _, m := range mocks {
		for _, e := range m.spec.expectations {
			if sm, ok := e.Matcher.(*matchscenario.Matcher); ok {
				uses = append(uses, matchscenario.Use{MockID: m.ID(), Matcher: sm})
			}
		}
	}

	return slices.Values(uses), nil
}

// scenarioKey returns the selector of the key of the instance of a scenario a request plays in: tmpl rendered with the
// data of reply templates, and the parameters of path template t, of a mock that has one.
func (app *Server) scenarioKey(tmpl string, t *pathTemplate) (vlatte.Selector[*selectorInput], error) {
	compiled, err := app.config.TemplateEngine.Compile("scenario.key", tmpl)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, in *selectorInput) any {
		data := locals(in.values, nil, viewdata.NewParameters(ctx, app.params))
		if t != nil {
			data.Request.PathParams, _ = t.params(in.URL.EscapedPath())
		}

		var key strings.Builder
		if err := compiled.Execute(&key, data); err != nil {
			return err
		}

		return key.String()
	}, nil
}

// movedScenarios keeps the moves of the scenarios of r's mock, which Find picked for in, for the journal, and publishes
// them to the admin API's events.
func (app *Server) movedScenarios(r *RequestValues, in *selectorInput) {
	for _, e := range r.Mock.spec.expectations {
		sm, ok := e.Matcher.(*matchscenario.Matcher)
		if !ok {
			continue
		}

		mv, ok := sm.Move(e.Arg(r.RawRequest.Context(), nil, in))
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
