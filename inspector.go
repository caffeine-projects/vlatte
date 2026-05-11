package vlatte

type Inspector[M Mock] interface {
	Hits() int64
	ListPending() []M
	ListCalled() []M
}
