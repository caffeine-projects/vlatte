// Package matchscenario serves mocks in turn, as a scenario moves from state to state.
//
// A [Store] keeps the state of each scenario of a mock server. Each mock of a scenario gets a [Matcher] from the
// store: it matches while the scenario is in the state the mock requires, and moves the scenario to the mock's new
// state once the mock answered. A scenario starts in [StateStarted]. Names ignore case: Checkout and checkout are one
// scenario.
//
// A mock server builds on it by giving each server one store, adding the matcher of a mock to its expectations, and
// calling [vlatte.Stateful.SetState] on it after the mock answered. The suite of
// [github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest] checks a server does.
package matchscenario
