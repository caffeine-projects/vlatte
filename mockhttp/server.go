package mockhttp

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	_ Backend        = (*httptestBackend)(nil)
	_ BackendFactory = (*httptestBackendFactory)(nil)
)

// BackendInfo describes a running Backend.
type BackendInfo struct {
	URL string
}

// Backend is the HTTP server that serves a mock server's handler. A mock server starts it in two steps: Listen or
// ListenTLS binds its address, so Info has its URL, while the server loads its mocks and starts its recording; then
// Serve answers the connections, those that came meanwhile included. It starts once.
type Backend interface {
	Listen() error
	ListenTLS() error
	Serve() error
	Close() error
	Info() *BackendInfo
}

// BackendFactory builds the Backend of a mock server.
// The default factory builds it on an httptest.Server.
type BackendFactory interface {
	NewBackend(config BackendConfig) (Backend, error)
}

// BackendConfig bundles the inputs a BackendFactory needs to build the Backend
// of a Server.
type BackendConfig struct {
	App     *Server
	Handler http.Handler
}

type httptestBackend struct {
	app     *Server
	handler http.Handler
	server  *httptest.Server
	info    atomic.Pointer[BackendInfo] // read by requests while Listen or Close sets it
	tls     bool                        // set by ListenTLS: Serve serves TLS
	rwMutex sync.RWMutex
}

type httptestBackendFactory struct{}

func newBackendFactory() BackendFactory {
	return &httptestBackendFactory{}
}

func (f *httptestBackendFactory) NewBackend(config BackendConfig) (Backend, error) {
	s := &httptestBackend{
		app:     config.App,
		handler: config.Handler,
	}
	s.info.Store(&BackendInfo{})

	return s, nil
}

func (s *httptestBackend) Listen() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.info.Store(&BackendInfo{URL: "http://" + s.server.Listener.Addr().String()})

	return nil
}

func (s *httptestBackend) ListenTLS() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.tls = true
	s.server.EnableHTTP2 = true
	s.info.Store(&BackendInfo{URL: "https://" + s.server.Listener.Addr().String()})

	return nil
}

// Serve serves what Listen or ListenTLS bound. It returns http.ErrServerClosed when Close came first.
func (s *httptestBackend) Serve() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if s.server == nil {
		return http.ErrServerClosed
	}

	if s.tls {
		s.server.StartTLS()
	} else {
		s.server.Start()
	}

	return nil
}

func (s *httptestBackend) Close() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if s.server == nil {
		return nil
	}

	s.server.CloseClientConnections()
	s.server.Close()
	s.server = nil
	s.info.Store(&BackendInfo{})

	return nil
}

func (s *httptestBackend) Info() *BackendInfo {
	return s.info.Load()
}

// build creates the unstarted httptest.Server, which listens already: on the configured Listener, on the configured
// address, or else on a random local port.
func (s *httptestBackend) build() error {
	s.server = httptest.NewUnstartedServer(s.handler)
	s.server.Config.ReadTimeout = s.app.config.ReadTimeout
	s.server.Config.ReadHeaderTimeout = s.app.config.ReadHeaderTimeout
	s.server.Config.IdleTimeout = s.app.config.IdleTimeout

	addr := s.app.config.Addr
	if l := s.app.config.Listener; l != nil {
		if err := s.server.Listener.Close(); err != nil {
			return err
		}

		s.server.Listener = l
	} else if addr != "" {
		if _, err := strconv.Atoi(addr); err == nil {
			addr = ":" + addr
		}

		if err := s.server.Listener.Close(); err != nil {
			return err
		}

		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}

		s.server.Listener = listener
	}

	if s.app.config.TLSConfig != nil {
		s.server.TLS = s.app.config.TLSConfig
		s.server.EnableHTTP2 = s.app.config.UseHTTP2
	} else {
		s.server.TLS = &tls.Config{Certificates: s.app.config.TLSCertificates}

		if s.app.config.TLSClientCAs != nil {
			s.server.TLS.ClientCAs = s.app.config.TLSClientCAs
			s.server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		}

		s.server.EnableHTTP2 = s.app.config.UseHTTP2
	}

	return nil
}
