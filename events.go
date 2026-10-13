package vlatte

// OnComplete is the event of a mock that replied. Its listeners run each in a goroutine of its own, and the response
// ends once they all have: see EventRunner.
type OnComplete interface {
	EventArgs

	Mock() Mock
}
