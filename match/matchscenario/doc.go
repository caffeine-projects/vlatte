// Package matchscenario serves mocks in turn, as a scenario moves from state to state.
//
// A [Store] keeps the state of each scenario of a mock server. Each mock of a scenario gets a [Matcher] from the
// store: it matches while the scenario is in the state the mock requires, and moves the scenario to the mock's new
// state when the mock is picked to answer, before it answers. Two requests never take the same turn: of two requests
// in one state, one gets the mock that requires that state and moves it, and the other matches in the next. A mock
// that requires no state, or moves to none, takes no turn: every request it matches gets it. A scenario starts in
// [StateStarted]. Names ignore case: Checkout and checkout are one scenario.
//
// A mock server builds on it by giving each server one store, adding the matcher of a mock to its expectations, and
// finding mocks with [vlatte.Mocks.Find], which takes the turn of each [vlatte.Claimer]. The suite of
// [github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest] checks a server does.
package matchscenario
