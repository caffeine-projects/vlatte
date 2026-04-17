package vlatte

type ServerInfo struct {
	URL string
}

type Server interface {
	Start() error

	StartTLS() error

	Close() error

	Info() *ServerInfo
}

type ServerFactory[C any] interface {
	Server(config C) (Server, error)
}
