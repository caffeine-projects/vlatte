package vlatte

import "net/http"

type ServerInfo struct {
	URL string
}

type Server[A any] interface {
	Setup(app A, handler http.Handler) error

	Start() error

	StartTLS() error

	Close() error

	Info() *ServerInfo
}
