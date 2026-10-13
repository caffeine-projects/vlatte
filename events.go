package vlatte

// OnComplete is the event of a mock that replied. Its listeners run after the response, in the background, each in a
// goroutine of its own: see EventRunner.
type OnComplete interface {
	EventArgs

	Mock() Mock
}
