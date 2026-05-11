package vlatte

type OnComplete interface {
	EventArgs

	Mock() Mock
}
