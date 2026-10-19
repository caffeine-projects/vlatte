package mockhttp

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// Backend is the HTTP server that serves a mock server's handler.
type Backend interface {
	Start() error
	StartTLS() error
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
	app      *Server
	handler  http.Handler
	server   *httptest.Server
	info     atomic.Pointer[BackendInfo] // read by requests while a start may replace it
	listener net.Listener                // the configured Listener, until a start serves it
	rwMutex  sync.RWMutex
}

type httptestBackendFactory struct{}

func newBackendFactory() BackendFactory {
	return &httptestBackendFactory{}
}

func (f *httptestBackendFactory) NewBackend(config BackendConfig) (Backend, error) {
	s := &httptestBackend{
		app:      config.App,
		handler:  config.Handler,
		listener: config.App.config.Listener,
	}
	s.info.Store(&BackendInfo{})

	return s, nil
}

func (s *httptestBackend) Start() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.server.Start()
	s.info.Store(&BackendInfo{URL: s.server.URL})

	return nil
}

func (s *httptestBackend) StartTLS() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.server.EnableHTTP2 = true
	s.server.StartTLS()
	s.info.Store(&BackendInfo{URL: s.server.URL})

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

// build creates a fresh unstarted httptest.Server each time it is called.
// httptest.Server instances are not reusable after Close, so a new one must
// be constructed for every Start/StartTLS cycle.
func (s *httptestBackend) build() error {
	admPath := "/" + strings.Trim(s.app.config.AdminPath, "/")
	admin := http.StripPrefix(admPath, s.app.admin.Init())

	s.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the admin API is routed. Every other request, whatever its method, reaches the mock handler.
		if strings.HasPrefix(r.URL.Path, admPath+"/") {
			admin.ServeHTTP(w, r)
			return
		}

		s.handler.ServeHTTP(w, r)
	}))

	addr := s.app.config.Addr
	if l := s.app.config.Listener; l != nil {
		// Close closes a configured Listener, so only the first start serves it. Later starts listen on its address.
		addr = l.Addr().String()
	}

	if s.listener != nil {
		if err := s.server.Listener.Close(); err != nil {
			return err
		}

		s.server.Listener, s.listener = s.listener, nil
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
