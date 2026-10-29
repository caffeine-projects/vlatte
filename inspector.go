package vlatte

// Inspector reports the hits and the calls of a group of mocks, such as a [Scope] or a whole server, for [Times] and
// [NotCalled].
type Inspector[M Mock] interface {
	Hits() int64
	ListPending() []M
	ListCalled() []M
}
