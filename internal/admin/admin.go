// Package admin serves the admin API of a mock server: a RESTful HTTP server of its own, on its own address, the same
// for mock servers of every protocol. Package handler holds its units, one per operation; a server answers 501 to the
// operations its protocol can't do, so the API, and its OpenAPI document, stay the same. With Options.MCP, it serves the
// operations over MCP too, as tools, which leave out what the server can't do.
package admin

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	{pattern: "GET /livez"},
	{pattern: "GET /readyz"},
	{pattern: "GET /startupz"},
	{pattern: "GET /{$}"},
	{pattern: "GET /docs"},
	{pattern: "GET /openapi"},
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
	{pattern: "DELETE /scenarios/{name}", lacking: "keep scenarios"},
	{pattern: "GET /scenario-board"},
	{pattern: "POST /reset"},
	{pattern: "GET /events", lacking: "stream its events"},
	{pattern: "GET /logs", lacking: "stream its logs"},
	{pattern: "GET /requests", lacking: "keep a journal of requests"},
	{pattern: "QUERY /requests", lacking: "keep a journal of requests"},
	{pattern: "DELETE /requests", lacking: "keep a journal of requests"},
	{pattern: "GET /requests/{id}", lacking: "keep a journal of requests"},
	{pattern: "QUERY /requests/verify", lacking: "keep a journal of requests"},
	{pattern: "POST /mcp", lacking: "serve MCP"},
}

// _probes are the Kubernetes probes, which need no credentials: a probe that needs a secret turns a wrong one into a
// restart loop.
var _probes = []string{"GET /livez", "GET /readyz", "GET /startupz"}

var _ adminapi.Server = (*Server)(nil)

var (
	errNoMocks = errors.New("admin: no mocks: the capabilities need Mocks")
	errNoMCP   = errors.New("admin: MCP is off: set Options.MCP")
	errClosing = errors.New("admin: the admin API is closing")
)

// _operations are the routes that run an admin operation, each with whether the mock server can do it: a route the
// server can't do answers 501.
var _operations = map[string]struct {
	serve func(ops *handler.Operations) http.Handler
	can   func(ops *handler.Operations) bool
}{
	"GET /health":              {serve: handler.Health},
	"GET /schema":              {serve: handler.MockSchema, can: (*handler.Operations).HasMockSchema},
	"GET /mocks":               {serve: handler.ListMocks},
	"POST /mocks":              {serve: handler.AddMock, can: (*handler.Operations).Builds},
	"DELETE /mocks":            {serve: handler.RemoveAllMocks},
	"POST /mocks/validate":     {serve: handler.ValidateMock, can: (*handler.Operations).Builds},
	"POST /mocks/reload":       {serve: handler.ReloadMocks, can: (*handler.Operations).HasFiles},
	"POST /mocks/save":         {serve: handler.SaveMocks, can: (*handler.Operations).HasFiles},
	"GET /mocks/{id}":          {serve: handler.GetMock},
	"PUT /mocks/{id}":          {serve: handler.ReplaceMock, can: (*handler.Operations).Builds},
	"PATCH /mocks/{id}":        {serve: handler.UpdateMock},
	"DELETE /mocks/{id}":       {serve: handler.RemoveMock},
	"GET /recording":           {serve: handler.RecordingStatus, can: (*handler.Operations).Records},
	"POST /recording":          {serve: handler.StartRecording, can: (*handler.Operations).Records},
	"DELETE /recording":        {serve: handler.StopRecording, can: (*handler.Operations).Records},
	"GET /scenarios":           {serve: handler.ListScenarios, can: (*handler.Operations).HasScenarios},
	"PUT /scenarios/{name}":    {serve: handler.SetScenario, can: (*handler.Operations).HasScenarios},
	"DELETE /scenarios":        {serve: handler.ResetScenarios, can: (*handler.Operations).HasScenarios},
	"DELETE /scenarios/{name}": {serve: handler.ResetScenario, can: (*handler.Operations).HasScenarios},
	"POST /reset":              {serve: handler.Reset},
	"GET /requests":            {serve: handler.ListRequests, can: (*handler.Operations).HasJournal},
	"QUERY /requests":          {serve: handler.QueryRequests, can: (*handler.Operations).HasJournal},
	"DELETE /requests":         {serve: handler.ClearRequests, can: (*handler.Operations).HasJournal},
	"GET /requests/{id}":       {serve: handler.GetRequest, can: (*handler.Operations).HasJournal},
	"QUERY /requests/verify":   {serve: handler.VerifyRequests, can: (*handler.Operations).HasJournal},
}

// Server is the admin API of a mock server.
type Server struct {
	addr    string
	tls     *tls.Config // nil serves plain HTTP
	auth    bool        // whether requests need credentials
	logger  *zerolog.Logger
	streams []func() // end the streams of Server-Sent Events routes
	ops     *handler.Operations
	handler http.Handler
	mcp     *mcp.Server // nil when MCP is off

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

	// MCP serves the admin operations over MCP too: Streamable HTTP at POST /mcp, and stdio with [Server.ServeMCP].
	MCP bool

	// Middlewares wrap every HTTP request of the admin API, the first outermost. They run before the cross-origin
	// check and the routes, and not for MCP over stdio.
	Middlewares []func(http.Handler) http.Handler

	// DocsVersion is the version of Scalar the docs page loads. Empty is the version vlatte pins.
	DocsVersion string

	// Auth are the credentials every HTTP request needs, but those of the probes. The zero value asks for none. They
	// are checked after the middlewares, so one can answer a CORS preflight, which carries none.
	Auth handler.Credentials

	// TLS serves the admin API over HTTPS with its certificates. Nil serves plain HTTP.
	TLS *tls.Config
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

	s := &Server{addr: opts.Addr, tls: opts.TLS, auth: opts.Auth.Required(), logger: caps.Logger}
	if s.logger == nil {
		s.logger = new(zerolog.Nop())
	}

	ops := handler.NewOperations(caps)
	s.ops = ops

	routes := map[string]http.Handler{
		"GET /{$}":            handler.Home(),
		"GET /docs":           handler.Docs(opts.DocsVersion),
		"GET /openapi":        handler.OpenAPI(),
		"GET /recorder":       handler.RecorderPage(),
		"GET /scenario-board": handler.ScenarioBoardPage(),
		"GET /livez":          handler.Live(ops, s.auth),
		"GET /readyz":         handler.Ready(ops, s.auth),
		"GET /startupz":       handler.Started(ops, s.auth),
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

	if opts.MCP {
		s.mcp = handler.NewMCP(ops)
		s.mcp.AddReceivingMiddleware(s.trackMCP)
		routes["POST /mcp"] = handler.MCPHandler(s.mcp)
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
	h := csrf.Handler(handler.Routing(mux))
	if s.auth {
		h = handler.Authenticate(opts.Auth, _probes, h)
	}

	for _, middleware := range slices.Backward(opts.Middlewares) {
		h = middleware(h)
	}

	s.handler = s.track(h)

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

	if s.tls != nil {
		srv.TLSConfig = s.tls.Clone()
	}

	s.srv, s.lis, s.served = srv, lis, served
	s.warnExposed()

	go func() {
		defer close(served)

		var err error
		if srv.TLSConfig != nil {
			err = srv.ServeTLS(lis, "", "")
		} else {
			err = srv.Serve(lis)
		}

		if !errors.Is(err, http.ErrServerClosed) {
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

	if s.tls != nil {
		return "https://" + s.lis.Addr().String()
	}

	return "http://" + s.lis.Addr().String()
}

// Close stops the admin API, and waits for the requests and MCP calls it serves to end, so no admin operation runs,
// and no connection stays, once it returns. It closes the connections of requests that take longer than a few seconds. A
// closed server doesn't start again.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	srv, served := s.srv, s.served
	s.mu.Unlock()

	// An operation that waits, such as a check of the journal, never holds up Close.
	s.ops.EndWaits()

	if srv == nil {
		// A server that never started may still serve MCP calls over stdio.
		s.running.Wait()
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

// ServeMCP serves the admin operations over MCP on in and out, newline-delimited JSON-RPC, as a client that starts
// the mock server as a process talks to it, until in ends or ctx is done. It needs no Start, and Close waits for the
// calls it serves. It fails when MCP is off.
func (s *Server) ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.mcp == nil {
		return errNoMCP
	}

	return handler.ServeMCP(ctx, s.mcp, in, out)
}

// warnExposed warns when the admin API listens beyond the local host without authentication, or sends credentials
// in plain text there: anyone on the network could change the mocks, or read the credentials.
func (s *Server) warnExposed() {
	addr, ok := s.lis.Addr().(*net.TCPAddr)
	if !ok || addr.IP.IsLoopback() {
		return
	}

	switch {
	case !s.auth:
		s.logger.Warn().Str("addr", addr.String()).
			Msg("admin: the admin API listens beyond the local host without authentication: anyone who reaches it can change the mocks")
	case s.tls == nil:
		s.logger.Warn().Str("addr", addr.String()).
			Msg("admin: the admin API listens beyond the local host over plain HTTP: anyone on the network can read its credentials")
	}
}

// trackMCP counts the MCP calls next handles, whichever transport they come on, so Close waits for them, and gives
// them the server's logger. A call that comes once Close runs fails: no operation starts then.
func (s *Server) trackMCP(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errClosing
		}

		s.running.Add(1)
		s.mu.Unlock()

		defer s.running.Done()

		return next(s.logger.WithContext(ctx), method, req)
	}
}

// track counts the requests h serves, so Close waits for them, and gives them the server's logger. A request that
// starts once Close runs gets a 503: no operation starts then. Every answer tells browsers not to sniff its type, as
// answers hold what clients sent, such as the requests of the journal.
func (s *Server) track(h http.Handler) http.Handler {
	closing := handler.Closing()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")

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
