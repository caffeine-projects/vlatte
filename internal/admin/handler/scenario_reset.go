package handler

// ResetScenarios moves every scenario back to where it started. It needs a server that keeps scenarios.
func (o *Operations) ResetScenarios() error {
	if o.scenarios == nil {
		return notSupported(_lackingScenarios)
	}

	o.scenarios.ResetScenarios()

	return nil
}

// ResetScenario moves the scenario name back to where it started. Names ignore case: Checkout is checkout. It needs a
// server that keeps scenarios.
func (o *Operations) ResetScenario(name string) error {
	if o.scenarios == nil {
		return notSupported(_lackingScenarios)
	}

	o.scenarios.ResetScenario(name)

	return nil
}
