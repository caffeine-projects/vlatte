package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/journal"
)

var (
	_ vlatte.Flavor[*Mock, *Server] = (*Server)(nil)
	_ grpc.ServiceRegistrar         = (*Server)(nil)
)

// ErrStreamInterceptorSlot is the error of a server with a journal whose ServerOptions set the stream interceptor
// with [grpc.StreamInterceptor], which grpc runs before every other: the journal takes it, to journal the code a call
// ends with. [grpc.ChainStreamInterceptor] gives the same interceptor.
var ErrStreamInterceptorSlot = errors.New("journal: pass stream interceptors with grpc.ChainStreamInterceptor: " +
	"the journal takes grpc.StreamInterceptor")

// Kind is the protocol of gRPC mock servers, the Kind of their journal entries.
const Kind vlatte.Kind = "grpc"

type Server struct {
	*vlatte.Base[*Mock, *Server]
	*journal.Verifier[*Mock, *Server]

	server *grpc.Server
	config *Config

	// journaled names the services whose unary calls the journal sees after every interceptor: those registered
	// through the server, not on GRPCServer. Registering ends before Start.
	journaled map[string]bool

	logger  *zerolog.Logger
	storage vlatte.Store[*Mock]
	admin   *admin.Server   // nil without Config.Admin
	events  *handler.Events // the admin API's; nil without it

	mu      sync.Mutex // orders Start and Close, which a Close overlapping a Start would otherwise outrun
	addr    string
	served  chan struct{}
	started bool // set by the first start: a server starts once
	closed  bool // set by Close: a closed server doesn't start again
}

// New creates a new gRPC mock Server with the given configurations.
// It returns an error if a configuration fails to apply.
func New(config ...Option) (*Server, error) {
	app := &Server{}
	conf := defaultConfig()

	for i, configurer := range config {
		err := configurer.Apply(conf)
		if err != nil {
			return nil, fmt.Errorf(
				"server: error applying configuration at index %d with type %T\n%w",
				i,
				configurer,
				err,
			)
		}
	}

	store := vlatte.NewStore[*Mock]()
	app.config = conf
	app.logger = conf.Logger

	// The admin API streams the log lines of a logger the server builds, not those of one it was given.
	var logs *handler.Logs
	if app.logger == nil {
		var out io.Writer = os.Stdout
		if conf.Admin != nil {
			logs = handler.NewLogs()
			out = zerolog.MultiLevelWriter(os.Stdout, logs)
		}

		l := zerolog.New(out).With().Timestamp().Logger()
		app.logger = &l
	}

	j, err := app.openJournal(conf)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	// A server that fails to build closes its journal: a temporary one goes.
	built := false
	defer func() {
		if !built {
			j.Close()
		}
	}()

	in := &interceptors{app: app}
	unary := []grpc.UnaryServerInterceptor{in.recoverUnary, in.UnaryInterceptor}
	stream := []grpc.StreamServerInterceptor{in.recoverStream, in.StreamInterceptor}

	// The journal sees each call as it ends, with the code the client gets, outside every interceptor of the user's
	// options. A unary call's journals within its method's handler, where grpc runs every unary interceptor: see
	// register. A stream's takes the stream interceptor grpc runs before every other. The unary one in the chain is
	// for services registered on GRPCServer, past the server.
	var opts []grpc.ServerOption
	if j != nil {
		opts = append(opts, grpc.StreamInterceptor(in.journalStream))
		unary = slices.Insert(unary, 0, in.journalUnary)
	}

	// User options come next: the mock interceptors never call the next handler, so they must run last.
	// The recovery interceptors run just before them, so a panic serving a mock answers the call.
	opts = append(opts, conf.ServerOptions...)
	opts = append(opts, grpc.ChainUnaryInterceptor(unary...), grpc.ChainStreamInterceptor(stream...))

	srv, err := newGRPCServer(opts, j != nil)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	app.Base = vlatte.NewBase(app, store)
	app.Verifier = journal.NewVerifier[*Mock, *Server](j, nil)
	app.storage = store
	app.server = srv
	app.journaled = make(map[string]bool)

	if conf.ServiceDesc != nil && conf.Service != nil {
		app.register(conf.ServiceDesc, conf.Service)
	}

	// The admin API's options have Mocks: it builds.
	if conf.Admin != nil {
		app.events = handler.NewEvents()
		app.admin = admin.MustNew(
			admin.Addr(conf.Admin.Addr),
			admin.Info(string(Kind), app.Addr),
			admin.Logger(app.logger),
			admin.Mocks[*Mock](store, nil),
			admin.Events(app.events),
			admin.Logs(logs),
			admin.Journal(j, nil, nil),
		)
	}

	built = true

	return app, nil
}

// MustNew is like New but panics if any error occurs.
func MustNew(config ...Option) *Server {
	app, err := New(config...)
	if err != nil {
		panic(err)
	}

	return app
}

// Prep runs the given recipes on this mock server, in order.
// It stops at the first recipe that fails and returns its error.
func (app *Server) Prep(ctx context.Context, recipes ...Recipe) error {
	for i, recipe := range recipes {
		if err := recipe.Prep(ctx, &vlatte.RecipeInput[*Mock, *Server]{Flavor: app}); err != nil {
			return fmt.Errorf("server: error applying recipe at index %d with type %T\n%w", i, recipe, err)
		}
	}

	return nil
}

// Start starts the mock server. A server starts once: Start fails with vlatte.ErrStarted once it started, and with
// grpc.ErrServerStopped once it closed. Start is all or nothing: it serves once it listens and its admin API, if any,
// serves. A start that fails stops what started, closes the server, and leaves nothing listening, the listener of
// Config.Listener included.
func (app *Server) Start() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.closed {
		return fmt.Errorf("server: Start: %w", grpc.ErrServerStopped)
	}

	if app.started {
		return fmt.Errorf("server: Start: %w", vlatte.ErrStarted)
	}

	app.started = true

	lis := app.config.Listener
	if lis == nil {
		var err error
		lis, err = net.Listen("tcp", app.config.Addr)
		if err != nil {
			app.closed = true
			app.Journal().Close()

			return fmt.Errorf("server: Start: failed to create listener: %w", err)
		}
	}

	app.addr = lis.Addr().String()

	if app.admin != nil {
		if err := app.admin.Start(); err != nil {
			_ = lis.Close()
			app.addr = ""
			app.closed = true
			app.Journal().Close()

			return fmt.Errorf("server: Start: %w", err)
		}
	}

	app.warnUnjournaled()

	app.served = make(chan struct{})

	go func(served chan struct{}) {
		defer close(served)

		// Serve returns nil after GracefulStop, so any error is a real failure, but one: a Close that came between
		// this start and Serve stops the server first, and Serve then refuses to begin.
		if err := app.server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			app.logger.Error().Err(err).Msg("server: error serving")
		}
	}(app.served)

	return nil
}

// MustStart starts the mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStart() {
	err := app.Start()
	if err != nil {
		panic(err)
	}
}

// GRPCServer returns the underlying gRPC server, for features beyond registering services,
// such as server reflection. With a journal, a service registered on it has the entries of its unary calls journaled
// within the interceptors of ServerOptions, with the code the mocks answered with: [Server.Start] warns of it.
// [Server.RegisterService] journals the code the client gets.
func (app *Server) GRPCServer() *grpc.Server {
	return app.server
}

// RegisterService registers a service on the mock server, so generated code can take it directly:
//
//	pb.RegisterGreeterServer(m, pb.UnimplementedGreeterServer{})
//
// It implements [grpc.ServiceRegistrar] and must be called before Start. With a journal, the entries of its calls
// have the code the client gets, after every interceptor: register services here, not on [Server.GRPCServer].
func (app *Server) RegisterService(desc *grpc.ServiceDesc, impl any) {
	app.register(desc, impl)
}

// newGRPCServer builds the gRPC server of opts. grpc panics on an option that sets a stream interceptor once one
// is: with a journal, which sets it first, that is an option of the user's, which New refuses.
func newGRPCServer(opts []grpc.ServerOption, journaled bool) (srv *grpc.Server, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		if msg, _ := r.(string); journaled && strings.Contains(msg, "stream server interceptor was already set") {
			err = ErrStreamInterceptorSlot
			return
		}

		panic(r)
	}()

	return grpc.NewServer(opts...), nil
}

// AdminURL returns the base URL of the admin API, or "" when the server serves none, or hasn't started: see
// ConfigBuilder.Admin.
func (app *Server) AdminURL() string {
	if app.admin == nil {
		return ""
	}

	return app.admin.URL()
}

func (app *Server) Addr() string {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.addr
}

// Close stops the server, waiting for calls in flight to finish. It closes the admin API first, which waits for the
// operations it serves, and the journal last, which it removes if it is a temporary one: verify the calls before
// Close. A closed server doesn't start again, even one that never started.
func (app *Server) Close() {
	app.mu.Lock()
	app.closed = true
	served := app.served
	app.mu.Unlock()

	if app.admin != nil {
		if err := app.admin.Close(); err != nil {
			app.logger.Debug().Err(err).Msg("server: Close: closing the admin API")
		}
	}

	if served != nil {
		app.server.GracefulStop()
		<-served
	}

	if err := app.Journal().Close(); err != nil {
		app.logger.Warn().Err(err).Msg("server: Close: closing the journal")
	}
}

// publish sends an event of kind, of a call of target, a full method, to the admin API's clients: mock, the mock that
// matched, if one did, and err, what failed, if anything. Nothing is published without the admin API.
func (app *Server) publish(kind, target string, mock *Mock, err error) {
	if app.events == nil {
		return
	}

	e := handler.Event{Type: kind, Time: time.Now(), Target: target}
	if mock != nil {
		e.MockID = mock.ID()
	}

	if err != nil {
		e.Message = err.Error()
	}

	app.events.Publish(e)
}

// publishReply publishes the call of target whose mock replied err: an error with a gRPC status is the mock's answer,
// as replyError tells, and any other a failure.
func (app *Server) publishReply(target string, mock *Mock, err error) {
	if _, ok := status.FromError(err); ok {
		app.publish(handler.EventMatched, target, mock, nil)
		return
	}

	app.publish(handler.EventError, target, mock, err)
}

// register registers the service desc describes, of impl: with a journal, its unary methods journal their calls.
func (app *Server) register(desc *grpc.ServiceDesc, impl any) {
	if app.Journal() != nil {
		desc = app.journaledService(desc)
		app.journaled[desc.ServiceName] = true
	}

	app.server.RegisterService(desc, impl)
}

// warnUnjournaled warns of each service registered on GRPCServer, past the server, with unary methods: their entries
// have the code the mocks answered with, not the one the client gets after the interceptors of ServerOptions.
func (app *Server) warnUnjournaled() {
	if app.Journal() == nil {
		return
	}

	for name, info := range app.server.GetServiceInfo() {
		unary := slices.ContainsFunc(info.Methods, func(m grpc.MethodInfo) bool { return !m.IsClientStream && !m.IsServerStream })
		if unary && !app.journaled[name] {
			app.logger.Warn().Str("service", name).Msg("server: Start: a service registered on GRPCServer journals its " +
				"unary calls with the code the mocks answered with, before the interceptors: register it with RegisterService")
		}
	}
}
