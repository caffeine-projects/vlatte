package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// scenarioChange is the body of PUT /scenarios/{name}.
type scenarioChange struct {
	state string
}

var _scenarioChangeSchema = config.NewSchema(
	config.Field[scenarioChange]{
		Key: "state", Kind: config.String, Usage: "The state the scenario moves to.",
		Set: func(c *scenarioChange, v config.Value) error { c.state = v.Str; return nil },
	},
)

// SetScenario moves the scenario name to the state of doc, a document in format, such as {"state": "paid"}, and
// returns it: its shared instance, or the one in picks, such as matchscenario.Key("t1"). The mocks that require that
// state match next. Names ignore case: Checkout is checkout. A state none of the scenario's mocks names is an
// unknown_state problem. It needs a server that keeps scenarios.
func (o *Operations) SetScenario(
	ctx context.Context,
	name string,
	doc []byte,
	format string,
	in ...matchscenario.Instance,
) (Scenario, error) {
	if o.scenarios == nil {
		return Scenario{}, notSupported(_lackingScenarios)
	}

	var change scenarioChange
	if err := decodeBody(_scenarioChangeSchema, &change, doc, format); err != nil {
		return Scenario{}, invalid("the body", err)
	}

	return o.moveScenario(ctx, name, change.state, in...)
}

// moveScenario moves the scenario name to state, which can't be empty, and returns it: its shared instance, or the
// one in picks.
func (o *Operations) moveScenario(ctx context.Context, name, state string, in ...matchscenario.Instance) (Scenario, error) {
	if state == "" {
		return Scenario{}, invalid("the body", &config.Error{Problems: []config.Problem{{
			Code: config.CodeMissingValue, Origin: "admin request", Key: "state", Message: "state is required",
		}}})
	}

	err := o.scenarios.SetScenario(ctx, name, state, in...)
	switch {
	case errors.Is(err, matchscenario.ErrUnknownState):
		return Scenario{}, &problemError{status: http.StatusBadRequest, code: _codeUnknownState, detail: err.Error()}
	case err != nil:
		return Scenario{}, fmt.Errorf("admin: setting scenario %s: %w", name, err)
	}

	return o.scenario(ctx, name)
}

// scenario returns the scenario name, which the server has.
func (o *Operations) scenario(ctx context.Context, name string) (Scenario, error) {
	list, err := o.ListScenarios(ctx)
	if err != nil {
		return Scenario{}, err
	}

	for _, scn := range list.Scenarios {
		if strings.EqualFold(scn.Name, name) {
			return scn, nil
		}
	}

	return Scenario{}, fmt.Errorf("admin: scenario %s is not listed after it moved", name)
}
