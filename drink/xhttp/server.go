package xhttp

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/go-chi/chi/v5"
)

var (
	_ vlatte.Server                             = (*httpTestServer)(nil)
	_ vlatte.ServerFactory[ServerFactoryConfig] = (*httpTestServerFactory)(nil)
)

// ServerFactoryConfig bundles the inputs a ServerFactory needs to build a Server
// for an HTTPMockApp instance.
type ServerFactoryConfig struct {
	App     *HTTPMockApp
	Handler http.Handler
}

type httpTestServer struct {
	app     *HTTPMockApp
	handler http.Handler
	server  *httptest.Server
	info    *vlatte.ServerInfo
	rwMutex sync.RWMutex
}

type httpTestServerFactory struct{}

func newServerFactory() vlatte.ServerFactory[ServerFactoryConfig] {
	return &httpTestServerFactory{}
}

func (f *httpTestServerFactory) Server(config ServerFactoryConfig) (vlatte.Server, error) {
	s := &httpTestServer{
		app:     config.App,
		handler: config.Handler,
		info:    &vlatte.ServerInfo{},
	}
	return s, nil
}

// build creates a fresh unstarted httptest.Server each time it is called.
// httptest.Server instances are not reusable after Close, so a new one must
// be constructed for every Start/StartTLS cycle.
func (s *httpTestServer) build() error {
	admPath := s.app.config.AdminPath
	if !strings.HasPrefix(admPath, "/") {
		admPath = "/" + admPath
	}

	r := chi.NewRouter()
	r.Mount(admPath, s.app.admin.Init())
	r.Handle("/*", s.handler)

	s.server = httptest.NewUnstartedServer(r)

	if s.app.config.Addr != "" {
		addr := s.app.config.Addr
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

func (s *httpTestServer) Start() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.server.Start()
	s.info.URL = s.server.URL

	return nil
}

func (s *httpTestServer) StartTLS() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if err := s.build(); err != nil {
		return err
	}

	s.server.EnableHTTP2 = true
	s.server.StartTLS()
	s.info.URL = s.server.URL

	return nil
}

func (s *httpTestServer) Close() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	if s.server == nil {
		return nil
	}

	s.server.CloseClientConnections()
	s.server.Close()
	s.server = nil
	s.info.URL = ""

	return nil
}

func (s *httpTestServer) Info() *vlatte.ServerInfo {
	return s.info
}
