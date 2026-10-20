// Package admin serves the admin API of a mock server: a RESTful HTTP server of its own, on its own address, the same
// for mock servers of every protocol. Package handler holds its units, one per operation; a server answers 501 to the
// operations its protocol can't do, so the API, and its OpenAPI document, stay the same.
package admin

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
)

const (
	// _readHeaderTimeout bounds how long a client takes to send the headers of a request.
	_readHeaderTimeout = 10 * time.Second

	// _shutdownTimeout bounds how long Close waits for the requests being served, before it closes their connections.
	_shutdownTimeout = 5 * time.Second
)

// _routes lists the routes of the admin API, each with what a server that lacks it can't do. The OpenAPI document
// documents each.
var _routes = []struct {
	pattern string
	lacking string
}{
	{pattern: "GET /health"},
	{pattern: "GET /openapi"},
	{pattern: "GET /docs"},
	{pattern: "GET /mocks"},
	{pattern: "POST /mocks", lacking: "build mocks from documents"},
	{pattern: "DELETE /mocks"},
	{pattern: "POST /mocks/validate", lacking: "build mocks from documents"},
	{pattern: "POST /mocks/reload", lacking: "load mock files"},
	{pattern: "GET /mocks/{id}"},
	{pattern: "PUT /mocks/{id}", lacking: "build mocks from documents"},
	{pattern: "PATCH /mocks/{id}"},
	{pattern: "DELETE /mocks/{id}"},
	{pattern: "GET /recording", lacking: "record"},
	{pattern: "POST /recording", lacking: "record"},
	{pattern: "DELETE /recording", lacking: "record"},
	{pattern: "GET /recorder"},
}

var errNoMocks = errors.New("admin: no mock store: pass the Mocks option")

// Server is the admin API of a mock server.
type Server struct {
	addr    string
	kind    string
	url     func() string
	logger  *zerolog.Logger
	routes  map[string]http.Handler
	handler http.Handler

	mu      sync.Mutex // orders Start, Close and the requests that start as Close runs
	srv     *http.Server
	lis     net.Listener
	served  chan struct{}
	running sync.WaitGroup // the requests being served
	started bool
	closed  bool
}

// Option configures a Server.
type Option func(s *Server)

// Addr sets the address the admin API listens on: host:port, :port or a port. Empty is a random local port.
func Addr(addr string) Option {
	return func(s *Server) { s.addr = addr }
}

// Info sets what GET /health tells of the mock server: its kind, such as http, and the URL it serves its mocks at.
func Info(kind string, url func() string) Option {
	return func(s *Server) { s.kind, s.url = kind, url }
}

// Logger sets the logger of the admin API's internal errors. Nil logs nothing.
func Logger(logger *zerolog.Logger) Option {
	return func(s *Server) { s.logger = logger }
}

// Mocks sets the store of the mock server's mocks, and the builder of the mocks that requests send, as mock files in
// JSON or YAML. Without a builder, the routes that add or replace mocks answer 501.
func Mocks[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M]) Option {
	return func(s *Server) {
		s.routes["GET /mocks"] = handler.ListMocks(store)
		s.routes["DELETE /mocks"] = handler.RemoveAllMocks(store)
		s.routes["GET /mocks/{id}"] = handler.GetMock(store)
		s.routes["PATCH /mocks/{id}"] = handler.UpdateMock(store)
		s.routes["DELETE /mocks/{id}"] = handler.RemoveMock(store)

		if build != nil {
			s.routes["POST /mocks"] = handler.AddMock(store, build)
			s.routes["POST /mocks/validate"] = handler.ValidateMock(store, build)
			s.routes["PUT /mocks/{id}"] = handler.ReplaceMock(store, build)
		}
	}
}

// Files sets the mock files of the mock server, which POST /mocks/reload loads again.
func Files(files handler.Files) Option {
	return func(s *Server) { s.routes["POST /mocks/reload"] = handler.ReloadMocks(files) }
}

// Recorder sets the recorder of the mock server, which GET, POST and DELETE /recording control.
func Recorder(rec handler.Recorder) Option {
	return func(s *Server) {
		s.routes["GET /recording"] = handler.RecordingStatus(rec)
		s.routes["POST /recording"] = handler.StartRecording(rec)
		s.routes["DELETE /recording"] = handler.StopRecording(rec)
	}
}

// New creates the admin API of a mock server. It needs the Mocks option.
func New(options ...Option) (*Server, error) {
	s := &Server{routes: make(map[string]http.Handler, len(_routes))}
	for _, option := range options {
		option(s)
	}

	if s.routes["GET /mocks"] == nil {
		return nil, errNoMocks
	}

	if s.logger == nil {
		s.logger = new(zerolog.Nop())
	}

	s.routes["GET /health"] = handler.Health(s.kind, s.url)
	s.routes["GET /openapi"] = handler.OpenAPI()
	s.routes["GET /docs"] = handler.Docs()
	s.routes["GET /recorder"] = handler.RecorderPage()

	mux := http.NewServeMux()
	for _, route := range _routes {
		h := s.routes[route.pattern]
		if h == nil {
			h = handler.NotSupported(route.lacking)
		}

		mux.Handle(route.pattern, h)
	}

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(handler.CrossOriginDenied())
	s.handler = s.track(csrf.Handler(handler.Routing(mux)))

	return s, nil
}

// MustNew is like New, but panics on an error.
func MustNew(options ...Option) *Server {
	s, err := New(options...)
	if err != nil {
		panic(err)
	}

	return s
}

// Start listens on the server's address and serves the admin API. It starts once: it fails with vlatte.ErrStarted
// once it started, and with http.ErrServerClosed once it closed. A start that fails to listen closes the server.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return http.ErrServerClosed
	}

	if s.started {
		return vlatte.ErrStarted
	}

	s.started = true

	addr := cmp.Or(s.addr, "127.0.0.1:0")
	if _, err := strconv.Atoi(addr); err == nil {
		addr = ":" + addr
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		s.closed = true
		return fmt.Errorf("admin: %w", err)
	}

	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: _readHeaderTimeout}
	served := make(chan struct{})

	s.srv, s.lis, s.served = srv, lis, served

	go func() {
		defer close(served)

		if err := srv.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error().Err(err).Msg("admin: serving")
		}
	}()

	return nil
}

// URL returns the base URL of the admin API, or "" before it starts.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lis == nil {
		return ""
	}

	return "http://" + s.lis.Addr().String()
}

// Close stops the admin API, and waits for the requests it serves to end, so no admin operation runs, and no
// connection stays, once it returns. It closes the connections of requests that take longer than a few seconds. A
// closed server doesn't start again.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	srv, served := s.srv, s.served
	s.mu.Unlock()

	if srv == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), _shutdownTimeout)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err != nil {
		err = srv.Close()
	}

	s.running.Wait()
	<-served

	return err
}

// track counts the requests h serves, so Close waits for them, and gives them the server's logger. A request that
// starts once Close runs is aborted: Close closed its connection.
func (s *Server) track(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			panic(http.ErrAbortHandler)
		}

		s.running.Add(1)
		s.mu.Unlock()

		defer s.running.Done()

		h.ServeHTTP(w, r.WithContext(s.logger.WithContext(r.Context())))
	})
}
