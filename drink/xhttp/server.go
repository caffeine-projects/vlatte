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
	_ vlatte.Server                              = (*httpTestServer)(nil)
	_ vlatte.ServerFactory[ServerFactoryConfig] = (*httpTestServerFactory)(nil)
)

// ServerFactoryConfig bundles the inputs a ServerFactory needs to build a Server
// for an HTTPMockApp instance.
type ServerFactoryConfig struct {
	App     *HTTPMockApp
	Handler http.Handler
}

type httpTestServer struct {
	server  *httptest.Server
	info    *vlatte.ServerInfo
	rwMutex sync.RWMutex
}

type httpTestServerFactory struct{}

func newServerFactory() vlatte.ServerFactory[ServerFactoryConfig] {
	return &httpTestServerFactory{}
}

func (f *httpTestServerFactory) Server(config ServerFactoryConfig) (vlatte.Server, error) {
	s := &httpTestServer{info: &vlatte.ServerInfo{}}
	if err := s.setup(config.App, config.Handler); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *httpTestServer) setup(app *HTTPMockApp, handler http.Handler) error {
	admPath := app.config.AdminPath
	if !strings.HasPrefix(admPath, "/") {
		admPath = "/" + admPath
	}

	r := chi.NewRouter()
	r.Mount(admPath, app.admin.Init())
	r.Handle("/*", handler)

	s.server = httptest.NewUnstartedServer(r)

	if app.config.Addr != "" {
		addr := app.config.Addr
		_, err := strconv.Atoi(addr)
		if err == nil {
			addr = ":" + addr
		}

		err = s.server.Listener.Close()
		if err != nil {
			return err
		}

		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}

		s.server.Listener = listener
	}

	if app.config.TLSConfig != nil {
		s.server.TLS = app.config.TLSConfig
		s.server.EnableHTTP2 = app.config.UseHTTP2
	} else {
		s.server.TLS = &tls.Config{Certificates: app.config.TLSCertificates}

		if app.config.TLSClientCAs != nil {
			s.server.TLS.ClientCAs = app.config.TLSClientCAs
			s.server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		}

		s.server.EnableHTTP2 = app.config.UseHTTP2
	}

	return nil
}

func (s *httpTestServer) Start() error {
	s.server.Start()
	s.info.URL = s.server.URL

	return nil
}

func (s *httpTestServer) StartTLS() error {
	s.server.EnableHTTP2 = true
	s.server.StartTLS()
	s.info.URL = s.server.URL

	return nil
}

func (s *httpTestServer) Close() error {
	s.rwMutex.Lock()
	defer s.rwMutex.Unlock()

	s.server.CloseClientConnections()
	s.server.Close()

	s.server.URL = ""

	return nil
}

func (s *httpTestServer) Info() *vlatte.ServerInfo {
	return s.info
}
