package vlatte

type OnComplete interface {
	EventArgs

	Bean() Bean
}
