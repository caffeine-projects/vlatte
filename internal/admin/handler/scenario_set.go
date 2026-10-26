package handler

import (
	"github.com/caffeine-projects/vlatte/internal/config"
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
// returns it. The mocks that require that state match next. Names are exact: Checkout isn't checkout. It needs a
// server that keeps scenarios.
func (o *Operations) SetScenario(name string, doc []byte, format string) (Scenario, error) {
	if o.scenarios == nil {
		return Scenario{}, notSupported(_lackingScenarios)
	}

	var change scenarioChange
	if err := decodeBody(_scenarioChangeSchema, &change, doc, format); err != nil {
		return Scenario{}, invalid("the body", err)
	}

	if change.state == "" {
		return Scenario{}, invalid("the body", &config.Error{Problems: []config.Problem{{
			Code: config.CodeMissingValue, Origin: "admin request", Key: "state", Message: "state is required",
		}}})
	}

	o.scenarios.SetScenario(name, change.state)

	return Scenario{Name: name, State: change.state}, nil
}
