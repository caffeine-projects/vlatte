package vlatte

// Stateful describes a matcher that has post processes that need to be executed
// after the matched mock is served. Dedicated for stateful matchers.
type Stateful interface {
	SetState() error
}
