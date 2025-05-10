package vlatte

// Kind describes the mock server implementation being used.
// E.g.: the HTTP mock server will return "http".
// The kind is useful to determine the mock server implementation within components like [PostAction], [Callback], etc.
// Also useful for a [Recipe] with different behaviors per mock server type.
type Kind string

func (k Kind) HTTP() bool {
	return k == "http"
}

func (k Kind) GRPC() bool {
	return k == "grpc"
}

func (k Kind) GraphQL() bool {
	return k == "graphql"
}

func (k Kind) String() string {
	return string(k)
}

// Built in [Kind] values.
const (
	KindHTTP    = Kind("http")
	KindGRPC    = Kind("grpc")
	KindGraphQL = Kind("graphql")
)
