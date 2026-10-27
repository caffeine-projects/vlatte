package mockgrpc

import (
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

var (
	_ vlatte.Server[*Mock, *Server] = (*Server)(nil)
	_ grpc.ServiceRegistrar         = (*Server)(nil)
)

// ErrStreamInterceptorSlot is the error of a server with a journal whose ServerOptions set the stream interceptor
// with [grpc.StreamInterceptor], which grpc runs before every other: the journal takes it, to journal the code a call
// ends with. [grpc.ChainStreamInterceptor] gives the same interceptor.
var ErrStreamInterceptorSlot = errors.New("journal: pass stream interceptors with grpc.ChainStreamInterceptor: " +
	"the journal takes grpc.StreamInterceptor")

var errAdminStoreWithoutAdmin = errors.New("server: AdminStore needs an admin API: pass ConfigBuilder.Admin")

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

	logger    *zerolog.Logger
	storage   vlatte.Store[*Mock]
	data      atomic.Pointer[map[string]any] // what templates read as .Data: see SetData
	resolver  *resolver
	scenarios *matchscenario.Store
	runner    *vlatte.EventRunner // runs the on_complete events of mocks
	admin     adminapi.Server     // nil without Config.Admin
	events    *adminapi.Events    // the admin API's; nil without it

	// adminStore writes the changes of the admin API through to its persistent store; nil without Config.AdminStore.
	adminStore *adminapi.Store[*Mock]

	// webhooks is the transport the server gives webhooks. Close closes its idle connections, so they don't outlive
	// the server.
	webhooks *http.Transport

	loader *fileLoader // loads the mock files of Config.MockFiles

	// recording is the recording calls are captured for, if any. recordMu orders its starts.
	recording atomic.Pointer[recording]
	recordMu  sync.Mutex

	// internal names the services the server implements itself, such as reflection: the mocks don't serve them.
	internal map[string]bool

	mu        sync.Mutex // orders Start and Close, which a Close overlapping a Start would otherwise outrun
	addr      string
	served    chan struct{}
	started   bool              // set by the first start: a server starts once
	closed    bool              // set by Close: a closed server doesn't start again
	memory    *bufconn.Listener // the listener of an in-memory server, once it started
	creds     *serverCreds      // TLS once StartTLS started the server
	ownsCreds bool              // the server handshakes with creds: not when ServerOptions set grpc.Creds
	tls       bool              // set by StartTLS
	cert      *x509.Certificate // the certificate StartTLS serves, when it knows it
	conns     []*grpc.ClientConn
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

	if err := loadConfigSources(conf); err != nil {
		return nil, fmt.Errorf("server: loading configuration sources: %w", err)
	}

	if conf.InMemory && conf.Listener != nil {
		return nil, fmt.Errorf("server: %w", errInMemoryListener)
	}

	// The server's record settings are its own: a caller's Config may be shared.
	if conf.Record != nil {
		rec := conf.Record.clone()
		if err := rec.check(); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}

		conf.Record = rec
	}

	if conf.NoMatchCode == codes.OK {
		conf.NoMatchCode = codes.NotFound
	}

	if conf.ShutdownTimeout <= 0 {
		conf.ShutdownTimeout = _defaultShutdownTimeout
	}

	switch {
	case conf.TemplateEngine == nil:
		e, err := vlatte.NewGoTemplateEngine(vlatte.WithEnv(conf.TemplateEnv...))
		if err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}

		conf.TemplateEngine = e
	case len(conf.TemplateEnv) > 0:
		return nil, fmt.Errorf("server: %w", errTemplateEnvWithEngine)
	}

	if conf.Parameters == nil {
		conf.Parameters = vlatte.NewMemoryKV()
	}

	if err := checkProviderKeys(conf.TemplateDataProviders); err != nil {
		return nil, err
	}

	// The admin API changes mocks through a store of its own when they persist: the server's other mocks, of mock
	// files and code, never reach the persistent store.
	if conf.AdminStore != nil && conf.Admin == nil {
		return nil, errAdminStoreWithoutAdmin
	}

	store := vlatte.NewStore[*Mock]()
	app.config = conf

	// The admin API streams the log lines of a logger the server builds, not those of one it was given.
	var logs *adminapi.Logs
	if conf.Admin != nil && conf.Logger == nil {
		logs = adminapi.NewLogs()
	}

	app.logger = newLogger(conf, logs)
	app.webhooks = httpx.NewWebhookTransport()

	res, err := newResolver(conf)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	app.resolver = res
	app.scenarios = matchscenario.New()

	global, err := app.prepareEvents(globalEvents(conf), "server: global on_complete listener")
	if err != nil {
		return nil, err
	}

	app.runner = vlatte.NewEventRunner(conf.Listeners, &vlatte.Events{OnComplete: global})

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

	// The server's credentials come first, so those of the user's options win: StartTLS sets TLS on them.
	app.creds = &serverCreds{TransportCredentials: insecure.NewCredentials()}
	opts := []grpc.ServerOption{grpc.Creds(app.creds)}

	// The journal sees each call as it ends, with the code the client gets, outside every interceptor of the user's
	// options. A unary call's journals within its method's handler, where grpc runs every unary interceptor: see
	// register. A stream's takes the stream interceptor grpc runs before every other. The unary one in the chain is
	// for services registered on GRPCServer, past the server.
	if j != nil {
		opts = append(opts, grpc.StreamInterceptor(in.journalStream))
		unary = slices.Insert(unary, 0, in.journalUnary)
	}

	// User options come next: the mock interceptors never call the next handler, so they must run last.
	// The recovery interceptors run just before them, so a panic serving a mock answers the call.
	opts = append(opts, conf.ServerOptions...)
	opts = append(opts,
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
		grpc.UnknownServiceHandler(unknownService),
	)

	srv, err := newGRPCServer(opts, j != nil)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	app.ownsCreds = keepsCreds(srv, app.creds)
	app.Base = vlatte.NewBase(app, store)
	app.Verifier = journal.NewVerifier[*Mock, *Server](j, app)
	app.storage = store
	app.server = srv
	app.journaled = make(map[string]bool)
	app.internal = make(map[string]bool)
	app.loader = &fileLoader{}

	// The recordings of the configured directory are served with the mock files.
	if conf.Record != nil {
		app.loader.saved = []string{conf.Record.dir(conf)}
	}

	if conf.Reflection {
		app.registerReflection()
	}

	if conf.ServiceDesc != nil && conf.Service != nil {
		app.register(conf.ServiceDesc, conf.Service)
	}

	if conf.Admin != nil {
		adminMocks := store

		if conf.AdminStore != nil {
			durable, err := conf.AdminStore(app)
			if err != nil {
				return nil, fmt.Errorf("server: admin store: %w", err)
			}

			app.adminStore = adminapi.NewStore(store, durable, app.logger)
			adminMocks = app.adminStore
		}

		app.events = adminapi.NewEvents()
		api, err := conf.Admin.Serve(adminapi.Capabilities{
			Kind:   string(Kind),
			URL:    app.Addr,
			Logger: app.logger,
			Mocks: adminapi.MocksOf(adminMocks, vlatte.MockBuilderFunc[*Mock](func(content []byte, format string) (*Mock, error) {
				return buildAdminMock(app, content, format)
			})),
			Files:     adminFiles{app},
			Recorder:  adminRecorder{app},
			Scenarios: app.scenarios,
			Events:    app.events,
			Logs:      logs,
			Journal:   j,
			Pattern:   app.adminRequestPattern,
			Matchers:  app.matchers(),
		})
		if err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}

		app.admin = api
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
		if err := recipe.Prep(ctx, &vlatte.RecipeInput[*Mock, *Server]{Server: app}); err != nil {
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
	return app.start("Start", false)
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

// Config returns the configuration of the server, as its options and sources left it. Changing it after New changes
// nothing the server set up already.
func (app *Server) Config() *Config {
	return app.config
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
// operations it serves. It cancels the on_complete events still running, and waits for those that run after the
// response. It closes the idle connections of the transport it gives webhooks, and the journal last, which it removes
// if it is a temporary one: verify the calls before Close. A closed server doesn't start again, even one that never
// started.
func (app *Server) Close() {
	app.mu.Lock()
	app.closed = true
	served := app.served
	conns := app.conns
	app.conns = nil
	app.mu.Unlock()

	if app.admin != nil {
		if err := app.admin.Close(); err != nil {
			app.logger.Debug().Err(err).Msg("server: Close: closing the admin API")
		}
	}

	// The calls wait for their events: cancel the events first. The connections Dial made end their calls.
	app.runner.Close()

	for _, conn := range conns {
		_ = conn.Close()
	}

	if served != nil {
		app.stop()
		<-served
	}

	// The recording logged each call it couldn't record. The server closes: it serves nothing more.
	_, _ = app.stopRecording(context.Background(), false)

	// The mocks stay stored, so assertions still read them after Close: they release what they hold.
	mocks, _ := app.storage.PickAll(context.Background())
	for _, mock := range mocks {
		if err := mock.Close(); err != nil {
			app.logger.Warn().Err(err).Msgf("server: Close: closing mock %s", mock.ID())
		}
	}

	app.webhooks.CloseIdleConnections()

	if err := app.Journal().Close(); err != nil {
		app.logger.Warn().Err(err).Msg("server: Close: closing the journal")
	}
}

// start starts the server, serving TLS when useTLS says: see Start. op names the method in its errors.
func (app *Server) start(op string, useTLS bool) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.closed {
		return fmt.Errorf("server: %s: %w", op, grpc.ErrServerStopped)
	}

	if app.started {
		return fmt.Errorf("server: %s: %w", op, vlatte.ErrStarted)
	}

	app.started = true

	if useTLS {
		if err := app.serveTLS(); err != nil {
			if app.config.Listener != nil {
				_ = app.config.Listener.Close()
			}

			app.closed = true
			app.Journal().Close()

			return fmt.Errorf("server: %s: %w", op, err)
		}
	}

	lis := app.config.Listener

	if app.config.InMemory {
		app.memory = bufconn.Listen(_bufSize)
		lis = app.memory
	}

	if lis == nil {
		var err error
		lis, err = net.Listen("tcp", app.config.Addr)
		if err != nil {
			app.closed = true
			app.Journal().Close()

			return fmt.Errorf("server: %s: failed to create listener: %w", op, err)
		}
	}

	app.addr = lis.Addr().String()

	if app.admin != nil {
		if err := app.admin.Start(); err != nil {
			_ = lis.Close()
			app.addr = ""
			app.closed = true
			app.Journal().Close()

			return fmt.Errorf("server: %s: %w", op, err)
		}
	}

	// The mock files load before the server serves, so its first call finds them, and so does the recording.
	err := app.load()
	if err == nil {
		err = app.startConfiguredRecording()
	}

	if err != nil {
		_ = lis.Close()
		app.addr = ""
		app.closed = true

		if app.admin != nil {
			_ = app.admin.Close()
		}

		app.Journal().Close()

		return fmt.Errorf("server: %s: %w", op, err)
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

// stop stops the gRPC server, waiting for the calls in flight up to Config.ShutdownTimeout, then cutting them: a
// stream whose client never ends it doesn't hold Close.
func (app *Server) stop() {
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		app.server.GracefulStop()
	}()

	t := time.NewTimer(app.config.ShutdownTimeout)
	defer t.Stop()

	select {
	case <-stopped:
	case <-t.C:
		app.logger.Warn().Dur("timeout", app.config.ShutdownTimeout).Msg("server: Close: cutting the calls in flight")
		app.server.Stop()
		<-stopped
	}
}

// publish sends an event of kind, of a call of target, a full method, to the admin API's clients: mock, the mock that
// matched, if one did, code, the code the call answered, and err, what failed, if anything. The event names the call's
// journal entry, when the server keeps a journal. Nothing is published without the admin API.
func (app *Server) publish(ctx context.Context, kind, target string, mock *Mock, code codes.Code, err error) {
	if app.events == nil {
		return
	}

	e := adminapi.Event{Type: kind, Time: time.Now(), Target: target, Status: int(code)}
	if mock != nil {
		e.MockID = mock.ID()
	}

	if call := callOf(ctx); call != nil {
		e.RequestID = call.draft.ID()
	}

	if err != nil {
		e.Message = err.Error()
	}

	app.events.Publish(e)
}

// register registers the service desc describes, of impl: with a journal, its unary methods journal their calls.
func (app *Server) register(desc *grpc.ServiceDesc, impl any) {
	if app.Journal() != nil {
		desc = app.journaledService(desc)
		app.journaled[desc.ServiceName] = true
	}

	app.server.RegisterService(desc, impl)
}

// matchers returns the registry that builds the matcher lists of the server's mock files: Config.Matchers, or the
// built-in matchers.
func (app *Server) matchers() *matchbuild.Registry {
	return cmp.Or(app.config.Matchers, _builtinMatchers)
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

// newLogger returns the logger of conf: its Logger, or one that writes to conf.LogOutput, the standard output by
// default, and to logs when the admin API streams them, at conf.LogLevel.
func newLogger(conf *Config, logs *adminapi.Logs) *zerolog.Logger {
	if conf.Logger != nil {
		return conf.Logger
	}

	out := conf.LogOutput
	if out == nil {
		out = os.Stdout
	}

	if logs != nil {
		out = zerolog.MultiLevelWriter(out, logs)
	}

	c := zerolog.New(out).Level(zerolog.Level(conf.LogLevel)).With().Timestamp()
	if conf.Name != "" {
		c = c.Str("server", conf.Name)
	}

	l := c.Logger()

	return &l
}

// newResolver returns the resolver of the descriptors conf gives, in Files and Descriptors.
func newResolver(conf *Config) (*resolver, error) {
	var files []*protoregistry.Files
	if conf.Files != nil {
		files = append(files, conf.Files)
	}

	if len(conf.Descriptors) > 0 {
		loaded, err := loadDescriptors(conf.Descriptors)
		if err != nil {
			return nil, err
		}

		files = append(files, loaded)
	}

	return newResolverOf(files...), nil
}

// globalEvents returns the global on_complete events of conf.
func globalEvents(conf *Config) vlatte.EventConfigs {
	if conf.GlobalEvents == nil {
		return nil
	}

	return conf.GlobalEvents.OnComplete
}

// prepareEvents returns a copy of events with their parameters as their listeners prepare them: see
// vlatte.EventPreparer. Each must name a registered listener, unless it carries its own: an event that names none
// would never run. scope starts the errors.
func (app *Server) prepareEvents(events vlatte.EventConfigs, scope string) (vlatte.EventConfigs, error) {
	if len(events) == 0 {
		return nil, nil
	}

	out := slices.Clone(events)
	listeners := app.config.Listeners

	for i, def := range out {
		if def.Listener != nil {
			continue
		}

		if listeners == nil || !listeners.OnComplete.Has(def.Name) {
			return nil, fmt.Errorf("%s %s at index %d is not registered", scope, def.Name, i)
		}

		params, err := listeners.OnComplete.Prepare(def, app.templateEngine())
		if err != nil {
			return nil, fmt.Errorf("%s %s at index %d: %w", scope, def.Name, i, err)
		}

		out[i].Parameters = params
	}

	return out, nil
}
