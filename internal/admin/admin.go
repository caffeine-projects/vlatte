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
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
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
	{pattern: "GET /schema", lacking: "describe its mock files"},
	{pattern: "GET /mocks"},
	{pattern: "POST /mocks", lacking: "build mocks from documents"},
	{pattern: "DELETE /mocks"},
	{pattern: "POST /mocks/validate", lacking: "build mocks from documents"},
	{pattern: "POST /mocks/reload", lacking: "load mock files"},
	{pattern: "POST /mocks/save", lacking: "save mock files"},
	{pattern: "GET /mocks/{id}"},
	{pattern: "PUT /mocks/{id}", lacking: "build mocks from documents"},
	{pattern: "PATCH /mocks/{id}"},
	{pattern: "DELETE /mocks/{id}"},
	{pattern: "GET /recording", lacking: "record"},
	{pattern: "POST /recording", lacking: "record"},
	{pattern: "DELETE /recording", lacking: "record"},
	{pattern: "GET /recorder"},
	{pattern: "GET /scenarios", lacking: "keep scenarios"},
	{pattern: "PUT /scenarios/{name}", lacking: "keep scenarios"},
	{pattern: "DELETE /scenarios", lacking: "keep scenarios"},
	{pattern: "POST /reset"},
	{pattern: "GET /events", lacking: "stream its events"},
	{pattern: "GET /logs", lacking: "stream its logs"},
	{pattern: "GET /requests", lacking: "keep a journal of requests"},
	{pattern: "QUERY /requests", lacking: "keep a journal of requests"},
	{pattern: "DELETE /requests", lacking: "keep a journal of requests"},
	{pattern: "GET /requests/{id}", lacking: "keep a journal of requests"},
}

var _ adminapi.Server = (*Server)(nil)

var errNoMocks = errors.New("admin: no mocks: the capabilities need Mocks")

// _operations are the routes that run an admin operation, each with whether the mock server can do it: a route the
// server can't do answers 501.
var _operations = map[string]struct {
	serve func(ops *handler.Operations) http.Handler
	can   func(ops *handler.Operations) bool
}{
	"GET /health":           {serve: handler.Health},
	"GET /schema":           {serve: handler.MockSchema, can: (*handler.Operations).HasMockSchema},
	"GET /mocks":            {serve: handler.ListMocks},
	"POST /mocks":           {serve: handler.AddMock, can: (*handler.Operations).Builds},
	"DELETE /mocks":         {serve: handler.RemoveAllMocks},
	"POST /mocks/validate":  {serve: handler.ValidateMock, can: (*handler.Operations).Builds},
	"POST /mocks/reload":    {serve: handler.ReloadMocks, can: (*handler.Operations).HasFiles},
	"POST /mocks/save":      {serve: handler.SaveMocks, can: (*handler.Operations).HasFiles},
	"GET /mocks/{id}":       {serve: handler.GetMock},
	"PUT /mocks/{id}":       {serve: handler.ReplaceMock, can: (*handler.Operations).Builds},
	"PATCH /mocks/{id}":     {serve: handler.UpdateMock},
	"DELETE /mocks/{id}":    {serve: handler.RemoveMock},
	"GET /recording":        {serve: handler.RecordingStatus, can: (*handler.Operations).Records},
	"POST /recording":       {serve: handler.StartRecording, can: (*handler.Operations).Records},
	"DELETE /recording":     {serve: handler.StopRecording, can: (*handler.Operations).Records},
	"GET /scenarios":        {serve: handler.ListScenarios, can: (*handler.Operations).HasScenarios},
	"PUT /scenarios/{name}": {serve: handler.SetScenario, can: (*handler.Operations).HasScenarios},
	"DELETE /scenarios":     {serve: handler.ResetScenarios, can: (*handler.Operations).HasScenarios},
	"POST /reset":           {serve: handler.Reset},
	"GET /requests":         {serve: handler.ListRequests, can: (*handler.Operations).HasJournal},
	"QUERY /requests":       {serve: handler.QueryRequests, can: (*handler.Operations).HasJournal},
	"DELETE /requests":      {serve: handler.ClearRequests, can: (*handler.Operations).HasJournal},
	"GET /requests/{id}":    {serve: handler.GetRequest, can: (*handler.Operations).HasJournal},
}

// Server is the admin API of a mock server.
type Server struct {
	addr    string
	logger  *zerolog.Logger
	streams []func() // end the streams of Server-Sent Events routes
	handler http.Handler

	mu      sync.Mutex // orders Start, Close and the requests that start as Close runs
	srv     *http.Server
	lis     net.Listener
	served  chan struct{}
	running sync.WaitGroup // the requests being served
	started bool
	closed  bool
}

// Options configure a Server.
type Options struct {
	// Addr is the address the admin API listens on: host:port, :port or a port. Empty is a random local port.
	Addr string
}

// New creates the admin API of the mock server of caps, which needs its Mocks.
func New(opts Options, caps adminapi.Capabilities) (*Server, error) {
	if caps.Mocks == nil {
		return nil, errNoMocks
	}

	// A server without a journal may pass its nil one: the routes answer 501, and reset skips it.
	if j, ok := caps.Journal.(*journal.Journal); ok && j == nil {
		caps.Journal = nil
	}

	s := &Server{addr: opts.Addr, logger: caps.Logger}
	if s.logger == nil {
		s.logger = new(zerolog.Nop())
	}

	ops := handler.NewOperations(caps)

	routes := map[string]http.Handler{
		"GET /openapi":  handler.OpenAPI(),
		"GET /docs":     handler.Docs(),
		"GET /recorder": handler.RecorderPage(),
	}

	if caps.Events != nil {
		routes["GET /events"] = caps.Events
		s.streams = append(s.streams, caps.Events.Close)
	}

	if caps.Logs != nil {
		routes["GET /logs"] = caps.Logs
		s.streams = append(s.streams, caps.Logs.Close)
	}

	for pattern, op := range _operations {
		if op.can == nil || op.can(ops) {
			routes[pattern] = op.serve(ops)
		}
	}

	mux := http.NewServeMux()
	for _, route := range _routes {
		h := routes[route.pattern]
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
func MustNew(opts Options, caps adminapi.Capabilities) *Server {
	s, err := New(opts, caps)
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

	// Streams never end on their own: end them, so the requests that read them do.
	for _, end := range s.streams {
		end()
	}

	ctx, cancel := context.WithTimeout(context.Background(), _shutdownTimeout)
	defer cancel()

	err := srv.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, srv.Close())
	}

	s.running.Wait()
	<-served

	return err
}

// track counts the requests h serves, so Close waits for them, and gives them the server's logger. A request that
// starts once Close runs gets a 503: no operation starts then.
func (s *Server) track(h http.Handler) http.Handler {
	closing := handler.Closing()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			closing.ServeHTTP(w, r)

			return
		}

		s.running.Add(1)
		s.mu.Unlock()

		defer s.running.Done()

		h.ServeHTTP(w, r.WithContext(s.logger.WithContext(r.Context())))
	})
}
