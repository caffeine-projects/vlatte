package vlatte

type Taste[M Bean] interface {
	Hits() int64
	ListPending() []M
	ListCalled() []M
}
