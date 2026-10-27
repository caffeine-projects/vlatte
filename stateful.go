package vlatte

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}

// Claimer is a matcher whose mock takes a turn when [Mocks.Find] picks it, before the mock answers, such as the turn
// of a scenario's state. Claim takes the turn, and reports false when another request took it since the matcher
// matched: Find then looks for a mock again. The next request sees the turn taken, even while the mock still answers,
// and the turn stays taken when the mock then fails to answer.
//
// Claim reports false only when Match would now report false, or Find looks again forever, until its context ends. A
// mock has at most one Claimer: a turn taken stays taken when the next Claimer of the mock loses its own.
type Claimer interface {
	Claim() bool
}
