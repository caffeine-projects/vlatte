package vlatte

// Kind describes the mock server implementation being used.
// E.g.: the HTTP mock server will return "http".
// The kind is useful to determine the mock server implementation within components like event listeners ([Listener], [OnComplete]), [Callback], etc.
// Also useful for a [Recipe] with different behaviors per mock server type.
type Kind string

const (
	KindHTTP    = Kind("http")
	KindGRPC    = Kind("grpc")
	KindGraphQL = Kind("graphql")
)

func (k Kind) HTTP() bool {
	return k == KindHTTP
}

func (k Kind) GRPC() bool {
	return k == KindGRPC
}

func (k Kind) GraphQL() bool {
	return k == KindGraphQL
}

func (k Kind) Is(other Kind) bool {
	return k == other
}

func (k Kind) String() string {
	return string(k)
}
