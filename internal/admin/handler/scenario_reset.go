package handler

import (
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// ResetScenarios moves every scenario back to where it started. It needs a server that keeps scenarios.
func (o *Operations) ResetScenarios() error {
	if o.scenarios == nil {
		return notSupported(_lackingScenarios)
	}

	o.scenarios.ResetScenarios()

	return nil
}

// ResetScenario moves the scenario name back to where it started: every instance of it, or only the one in picks, such
// as matchscenario.Key("t1"). Names ignore case: Checkout is checkout. It needs a server that keeps scenarios.
func (o *Operations) ResetScenario(name string, in ...matchscenario.Instance) error {
	if o.scenarios == nil {
		return notSupported(_lackingScenarios)
	}

	o.scenarios.ResetScenario(name, in...)

	return nil
}
