package vlatte

type Taste[M Mock] interface {
	Hits() int64
	ListPending() []M
	ListCalled() []M
}
