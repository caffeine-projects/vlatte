package mockhttp

import (
	"cmp"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mid"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mid/recovery"
	"github.com/caffeine-projects/vlatte/verify"
)

var _ vlatte.Server[*Mock, *Server] = (*Server)(nil)

var errAdminStoreWithoutAdmin = errors.New("server: AdminStore needs an admin API: pass ConfigBuilder.Admin")

// StatusNoMatch describes an HTTP response where no Mock was found.
//
// It uses http.StatusTeapot to reduce the chance of using the same
// expected response from the actual server being mocked.
// Basically, every request that doesn't match against a Mock, will have a response with http.StatusTeapot.
// It is also the default status of a failure, such as a reply that fails: see Config.ErrorStatusCode.
const StatusNoMatch = http.StatusTeapot

// Kind identifies HTTP mocks in protocol-neutral code, such as event listeners and callbacks.
const Kind vlatte.Kind = "http"

// Server is the base for the mock server.
type Server struct {
	*vlatte.Base[*Mock, *Server]
	*verify.Verifier[*Mock, *Server]

	config             *Config
	server             Backend
	storage            vlatte.Store[*Mock]
	scenarioStore      *matchscenario.Store
	eventStream        *adminapi.Events // the admin API's; nil without it
	requestBodyParsers []RequestBodyParser
	params             vlatte.KV
	loaders            []Loader
	rwMutex            sync.RWMutex
	proxy              *forwardProxy
	noMatch            NoMatchRenderer // answers the requests no mock matches

	// id tells the requests this server sends on, in their VLatte-Via, so one that comes back fails as a proxy loop.
	id string

	// tunnels holds the connections of the forward proxy's open tunnels, which Close closes.
	tunnels tunnels

	// proxyAll answers the requests no mock matches, when the setting proxy_all is set.
	proxyAll Replier

	// handler serves the requests, middlewares included, such as those within the tunnels the forward proxy
	// intercepts.
	handler http.Handler

	// recoverer answers a panic serving a request: the outermost middleware, and a mock that answers on the
	// connection it took over, which the middleware can't write to.
	recoverer *recovery.Recover

	data    map[string]any
	logger  *zerolog.Logger
	started atomic.Bool     // set by the first start: a server starts once
	closed  atomic.Bool     // set by Close: a closed server doesn't start again
	admin   adminapi.Server // nil without Config.Admin

	// adminStore writes the changes of the admin API through to its persistent store; nil without Config.AdminStore.
	adminStore *adminapi.Store[*Mock]

	// events runs the on_complete events of the mocks, which their responses wait for, and Close cancels them.
	events *vlatte.EventRunner

	// webhooks is the transport the server gives webhooks when its config has none. Close closes its idle connections,
	// so they don't outlive the server.
	webhooks *http.Transport

	// recording is the recording requests are captured for, if any. recordMu orders its starts, and guards played:
	// the directory of the last recording StopRecording ended, whose recordings files loads with the mock files. It
	// guards saved too: the directories the admin API saved mocks into, whose mock files files loads as well.
	recording atomic.Pointer[recording]
	recordMu  sync.Mutex
	played    string
	saved     []string
	files     *fileLoader
}

// New creates a new HTTP mock Server with the given configurations.
// Parameter config accepts a Config or a ConfigBuilder implementation.
// If no configuration is provided, a default one will be used.
// If no port is set, it will start the server on localhost using a random port.
// It returns an error if a configuration fails to apply or the server cannot be built.
func New(config ...Option) (*Server, error) {
	app := &Server{id: uuid.New().String()}

	conf := defaultConfig()

	for i, configurer := range config {
		err := configurer.Apply(conf)
		if err != nil {
			return nil, fmt.Errorf(
				"server: applying option %d (%T): %w",
				i,
				configurer,
				err,
			)
		}
	}

	if err := loadConfigSources(conf); err != nil {
		return nil, fmt.Errorf("server: loading the configuration sources: %w", err)
	}

	if err := conf.checkName(); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	if conf.DebugFn == nil {
		conf.DebugFn = func(_ string, _ any) {
			// Debug logging is off unless a DebugFn is given.
		}
	}

	if conf.RequestWasNotMatchedStatusCode == 0 {
		conf.RequestWasNotMatchedStatusCode = StatusNoMatch
	}

	if conf.ErrorStatusCode == 0 {
		conf.ErrorStatusCode = StatusNoMatch
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

	if err := vlatte.CheckTemplateDataProviders(conf.TemplateDataProviders); err != nil {
		return nil, err
	}

	noMatch, err := noMatchRendererOf(conf)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	app.config = conf
	app.noMatch = noMatch
	logs, tee := adminLogs(conf)
	app.logger = app.getLog(conf, tee)

	repository := vlatte.NewStore[*Mock]()

	parsers := make([]RequestBodyParser, 0, len(conf.RequestBodyParsers)+4)
	parsers = append(parsers, conf.RequestBodyParsers...)
	parsers = append(parsers, &plainTextParser{}, &formURLEncodedParser{}, &multipartFormParser{}, &noopParser{})

	app.recoverer = recovery.New(func(err error) { app.logger.Error().Err(err).Msg(err.Error()) },
		conf.ErrorStatusCode, http.Header{http.CanonicalHeaderKey(HeaderVlatteError): {vlatte.FailurePanic}})

	middlewares := make([]func(handler http.Handler) http.Handler, 0)
	middlewares = append(middlewares, app.recoverer.Recover)

	if conf.CORS != nil {
		middlewares = append(middlewares, corsMiddleware(conf.CORS))
	}

	middlewares = append(middlewares, conf.Middlewares...)

	var params vlatte.KV
	if conf.Parameters != nil {
		params = conf.Parameters
	} else {
		params = vlatte.NewMemoryKV()
	}

	if conf.Record != nil {
		record := conf.Record.clone()
		if err := record.check(); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}

		conf.Record = record
	}

	var p *forwardProxy
	if conf.Proxy != nil {
		var err error
		if p, err = newProxy(app.id, conf); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	}

	var lifecycle mockHTTPLifecycle
	if conf.Debug {
		out, terminal := app.logOutput(), false
		if f, ok := out.(*os.File); ok {
			terminal = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
			// On Windows consoles, colorable translates the escape codes.
			out = colorable.NewColorable(f)
		}

		lifecycle = newBuiltInDescriptiveMockHTTPLifecycle(app, ansi(colorsEnabled(conf.Colors, terminal)), out)
	} else {
		lifecycle = newBuiltInMockHTTPLifecycle(app)
	}

	events, lifecycle := withAdminEvents(conf, lifecycle)
	app.eventStream = events
	lifecycle = heldLifecycle{withJournal(app, conf, lifecycle)}

	globalEvents, err := prepareGlobalEvents(conf)
	if err != nil {
		return nil, err
	}

	eventRunner := vlatte.NewEventRunner(app.config.Listeners, globalEvents)
	handler := mid.Compose(middlewares...).Root(newMockHandler(app, lifecycle, conf.TemplateDataProviders, eventRunner))

	if conf.HandlerDecorator != nil {
		handler = conf.HandlerDecorator(handler)
	}

	// The decorator and the middlewares see the fragment in r.URL too. Outermost, the answer's writer sees the status
	// the client gets, whatever they send.
	handler = withAnswer(withFragment(handler))
	app.handler = handler

	factory := conf.BackendFactory

	if factory == nil {
		factory = newBackendFactory()
	}

	files := &fileLoader{}

	j, err := openJournal(conf, app.logger)
	if err != nil {
		return nil, err
	}

	// A server that fails to build closes its journal: a temporary one goes.
	built := false
	defer func() {
		if !built {
			j.Close()
		}
	}()

	app.Base = vlatte.NewBase(app, repository)
	app.Verifier = verify.NewVerifier(j, verify.Protocol[*Mock, *Server]{Patterns: app})
	app.storage = repository
	app.scenarioStore = matchscenario.New()
	if app.eventStream != nil {
		app.scenarioStore.Notify(app.publishScenario)
	}
	app.params = params
	app.loaders = conf.Loaders
	app.files = files
	app.proxy = p

	// The setting proxy_all answers what no mock matches: as a mock, it would count in assertions, and Clean and
	// Disable would drop it.
	if def := conf.proxyAll; def != nil {
		if app.proxyAll, err = def.build().NewReplier(app); err != nil {
			return nil, fmt.Errorf("server: proxy_all: %w", err)
		}
	}
	app.requestBodyParsers = parsers
	app.events = eventRunner
	app.webhooks = httpx.NewWebhookTransport(conf.OutboundAllow, conf.OutboundDeny)

	// The admin API changes mocks through a store of its own when they persist: the server's other mocks, of mock
	// files and code, never reach the persistent store.
	if conf.AdminStore != nil && conf.Admin == nil {
		return nil, errAdminStoreWithoutAdmin
	}

	if conf.Admin != nil {
		adminMocks := repository

		if conf.AdminStore != nil {
			durable, err := conf.AdminStore(app)
			if err != nil {
				return nil, fmt.Errorf("server: admin store: %w", err)
			}

			app.adminStore = adminapi.NewStore(repository, durable, app.logger)
			adminMocks = app.adminStore
		}

		app.admin, err = conf.Admin.Serve(adminapi.Capabilities{
			Kind:   string(Kind),
			URL:    func() string { return app.URL() },
			Logger: app.logger,
			Mocks: adminapi.MocksOf(adminMocks, vlatte.MockBuilderFunc[*Mock](func(content []byte, format string) (*Mock, error) {
				return buildAdminMock(app, content, format)
			})),
			Files:      adminFiles{app},
			Recorder:   adminRecorder{app},
			Scenarios:  app,
			Events:     events,
			Logs:       logs,
			Journal:    j,
			Pattern:    app.adminRequestPattern,
			Matchers:   app.matchers(),
			MockSchema: _mockFileSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	}

	server, err := factory.NewBackend(BackendConfig{App: app, Handler: handler})
	if err != nil {
		return nil, fmt.Errorf("server: building the backend: %w", err)
	}

	app.server = server
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

// prepareGlobalEvents returns the global events of conf with their parameters as their listeners prepare them, without
// changing conf's: see vlatte.EventPreparer. Like a mock's events, each must name a registered listener, unless it
// carries its own: an event that names none would never run.
func prepareGlobalEvents(conf *Config) (*vlatte.Events, error) {
	if conf.GlobalEvents == nil {
		return conf.GlobalEvents, nil
	}

	events := &vlatte.Events{OnComplete: slices.Clone(conf.GlobalEvents.OnComplete)}
	for i, def := range events.OnComplete {
		if def.Listener != nil {
			continue
		}

		if conf.Listeners == nil || !conf.Listeners.OnComplete.Has(def.Name) {
			return nil, fmt.Errorf("server: global on_complete listener %s at index %d is not registered", def.Name, i)
		}

		params, err := conf.Listeners.OnComplete.Prepare(def, conf.TemplateEngine)
		if err != nil {
			return nil, fmt.Errorf("server: global on_complete listener %s at index %d: %w", def.Name, i, err)
		}

		events.OnComplete[i].Parameters = params
	}

	return events, nil
}

// Prep runs the given recipes on this mock server, in order.
// It stops at the first recipe that fails and returns its error.
func (app *Server) Prep(ctx context.Context, recipes ...Recipe) error {
	for i, recipe := range recipes {
		if err := recipe.Prep(ctx, &vlatte.RecipeInput[*Mock, *Server]{Server: app}); err != nil {
			return fmt.Errorf("server: applying recipe %d (%T): %w", i, recipe, err)
		}
	}

	return nil
}

// Name returns mock server name.
func (app *Server) Name() string {
	return app.config.Name
}

// Start starts the mock server. It listens, so loaders can read URL, loads the mocks and starts the recording the
// config sets, then serves: the first request finds them. A server starts once: Start returns vlatte.ErrStarted once it
// started, and http.ErrServerClosed once it closed. Start is all or nothing: a start that fails to listen, load the
// mocks, start the recording or the admin API, or serve, stops what started and closes the server, so nothing listens,
// the listener of Config.Listener included.
func (app *Server) Start() error {
	return app.start(app.server.Listen)
}

// MustStart starts the mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStart() {
	err := app.Start()
	if err != nil {
		panic(fmt.Errorf("server: starting: %w", err))
	}
}

// StartTLS starts the mock server with TLS, as Start does.
func (app *Server) StartTLS() error {
	return app.start(app.server.ListenTLS)
}

// MustStartTLS starts TLS on a mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStartTLS() {
	err := app.StartTLS()
	if err != nil {
		panic(fmt.Errorf("server: starting with TLS: %w", err))
	}
}

// URL returns the base URL of the mock server. With paths, it joins them to it as path elements, with url.JoinPath:
// URL("/users", "1") is <base>/users/1, and "%3F" is a "?" in the path. A "?" or "#" in the last element starts the
// query or the fragment, which are appended as given, so they must already be escaped:
// URL("/users/1?active=true#top"). Go's http.Client doesn't send the fragment. With paths, it panics when
// [url.JoinPath] fails on the base URL.
func (app *Server) URL(paths ...string) string {
	if len(paths) == 0 {
		return app.server.Info().URL
	}

	// url.JoinPath would escape "?" and "#" into the path: cut the query and the fragment off first.
	elems := slices.Clone(paths)
	last := len(elems) - 1
	var rest string
	if i := strings.IndexAny(elems[last], "?#"); i >= 0 {
		elems[last], rest = elems[last][:i], elems[last][i:]
	}

	u, err := url.JoinPath(app.server.Info().URL, elems...)
	if err != nil {
		// A library must not exit the process: panic so the caller (usually a test) reports it.
		panic(fmt.Errorf("server: building server url with path elements %s: %w", paths, err))
	}

	return u + rest
}

// AdminURL returns the base URL of the admin API, or "" when the server serves none, or hasn't started: see
// ConfigBuilder.Admin.
func (app *Server) AdminURL() string {
	if app.admin == nil {
		return ""
	}

	return app.admin.URL()
}

// Parameters returns Params instance associated with this application.
func (app *Server) Parameters() vlatte.KV {
	return app.params
}

// Config returns the mock server Config.
func (app *Server) Config() *Config {
	return app.config
}

// Backend returns the Backend implementation being used by this application.
func (app *Server) Backend() Backend {
	return app.server
}

// Logger returns the zerolog.Logger of this application.
func (app *Server) Logger() *zerolog.Logger {
	return app.logger
}

// Reload loads the mocks of external sources, such as mock files and the admin API's persistent store, again.
// Each loader replaces the mocks it added before; mocks added in code are kept.
// The mocks of the mock files and of the persistent store are swapped in one step, and stay as they were when a file
// has problems, or the persistent store can't be read or its mocks served. The loaders of Config.Loaders run after.
func (app *Server) Reload() error {
	return app.load()
}

// MustReload reloads mocks from external sources, like the filesystem.
// Coded mocks will be kept.
// It panics if any error occurs.
func (app *Server) MustReload() {
	err := app.Reload()
	if err != nil {
		panic(fmt.Errorf("server: reloading the mocks: %w", err))
	}
}

// Close closes the mock server. It closes the admin API first, which waits for the operations it serves. It cancels the
// on_complete events still running, such as a webhook waiting for its delay or its target, so the requests waiting for
// them end, and waits for those requests and for the events that run after the response. It stops the recording, and
// waits until it wrote what it took, and closes the forward proxy's open tunnels. It closes the idle connections of the
// transports it made, such as the one of webhooks. Last, it closes the journal, and removes a temporary one: verify the
// requests before Close, as t.Cleanup(m.Close) leaves a test to. A closed server doesn't start again.
func (app *Server) Close() {
	app.closed.Store(true)

	if app.admin != nil {
		if err := app.admin.Close(); err != nil {
			app.logger.Debug().Err(err).Msg("server: Close: error closing the admin API")
		}
	}

	// The backend waits for the requests it serves, and they wait for their events: cancel the events first.
	app.events.Close()

	err := app.server.Close()
	if err != nil {
		app.logger.Debug().Err(err).Msg("server: Close: error closing server")
	}

	// The backend doesn't track the connections it handed over to tunnels, nor wait for their handlers, such as the
	// requests of intercepted connections: they end before the recording and the mocks do.
	app.tunnels.closeAll()

	// The recording logged each call it couldn't record. The server closes: it serves nothing more.
	_, _ = app.stopRecording(context.Background(), false)

	// The mocks stay stored, so assertions still read them after Close: they release what they hold.
	mocks, _ := app.storage.PickAll(context.Background())
	for _, mock := range mocks {
		if err := mock.Close(); err != nil {
			app.logger.Warn().Err(err).Msgf("server: Close: closing mock %s", mock.ID())
		}
	}

	if app.proxy != nil {
		app.proxy.close()
	}

	if closer, ok := app.proxyAll.(io.Closer); ok {
		_ = closer.Close()
	}

	app.webhooks.CloseIdleConnections()

	// Last: the requests the backend waited for are journaled. A temporary journal goes.
	if err := app.Journal().Close(); err != nil {
		app.logger.Warn().Err(err).Msg("server: Close: closing the journal")
	}
}

// ProxyCA returns the certificate of the CA that signs the certificates of the hosts the forward proxy intercepts, for
// clients to trust, or nil when it doesn't intercept. See ProxyConfig.Intercept.
func (app *Server) ProxyCA() *x509.Certificate {
	if app.proxy == nil || app.proxy.interceptor == nil {
		return nil
	}

	return app.proxy.interceptor.ca
}

// SetData sets the data templates read as .Data, such as the URL of another server: {{ .Data.target }}. The server
// keeps a copy of the map, and requests read the data in force when they arrive.
func (app *Server) SetData(data map[string]any) {
	data = maps.Clone(data)

	app.rwMutex.Lock()
	app.data = data
	app.rwMutex.Unlock()
}

// Data returns the data templates read as .Data. Requests read the map, so don't change it: set new data instead.
func (app *Server) Data() map[string]any {
	app.rwMutex.RLock()
	defer app.rwMutex.RUnlock()

	return app.data
}

// DescribeConfig describes key configurations of this Server.
func (app *Server) DescribeConfig() string {
	buf := bufpool.Get()
	buf.Reset()

	defer bufpool.Put(buf)

	if app.Name() != "" {
		buf.WriteString("Server Name: ")
		buf.WriteString(app.Name())
		buf.WriteString("\n")
	}

	buf.WriteString("Work Dir: ")
	buf.WriteString(app.config.Path(WorkDirVar))
	buf.WriteString("\n")

	buf.WriteString("Mock Search Patterns: ")
	buf.WriteString(strings.Join(app.config.MockFileSearchPatterns, ", "))
	buf.WriteString("\n")

	buf.WriteString("Log: ")
	buf.WriteString(app.config.LogVerbosity.String())
	buf.WriteString("\n")

	if app.config.Proxy != nil {
		buf.WriteString("Forward Proxy: ")
		buf.WriteString("enabled")

		if app.config.Proxy.Intercept {
			buf.WriteString(", intercepting HTTPS")
		}

		buf.WriteString("\n")
	}

	if def := app.config.proxyAll; def != nil {
		if target, err := checkTargetURL(def.target); err == nil {
			buf.WriteString("Proxy All: ")
			buf.WriteString(displayURL(target))
			buf.WriteString("\n")
		}
	}

	if app.config.Record != nil {
		buf.WriteString("Recording: ")
		buf.WriteString(app.config.Record.dir(app.config))
		buf.WriteString("\n")

		if target := app.config.Record.Target; target != "" {
			buf.WriteString("Recording Target: ")
			buf.WriteString(target)
			buf.WriteString("\n")
		}
	}

	buf.WriteString("\n")
	buf.WriteString("Listening: ")
	buf.WriteString(app.URL())
	buf.WriteString("\n")

	if app.admin != nil {
		buf.WriteString("Admin API: ")
		buf.WriteString(app.AdminURL())
		buf.WriteString("\n")
	}

	return buf.String()
}

// --
// Internals
// --

// matchers returns the registry that builds the matcher lists of the server's mock files: Config.Matchers, or the
// built-in matchers.
func (app *Server) matchers() *matchbuild.Registry {
	return cmp.Or(app.config.Matchers, _builtinMatchers)
}

// webhookTransport returns the transport webhooks send through when they have none of their own: Config.Transport,
// which is the caller's, or else the one the server made, which Close closes.
func (app *Server) webhookTransport() http.RoundTripper {
	if app.config.Transport != nil {
		return app.config.Transport
	}

	return app.webhooks
}

// start starts the server once with listen, the backend's Listen or ListenTLS. The backend listens first, so loaders
// can read URL, and serves once the mocks loaded, the recording started and the admin API serves. A start that fails,
// or that a Close overtook, closes the server.
func (app *Server) start(listen func() error) error {
	if app.closed.Load() {
		return http.ErrServerClosed
	}

	if !app.started.CompareAndSwap(false, true) {
		return vlatte.ErrStarted
	}

	err := listen()
	if err == nil {
		err = app.onStart()
	}

	if err == nil && app.admin != nil {
		err = app.admin.Start()
	}

	if err == nil && app.closed.Load() {
		err = http.ErrServerClosed
	}

	if err == nil {
		err = app.server.Serve()
	}

	if err != nil {
		app.Close()
		return err
	}

	return nil
}

// onStart loads the mocks, then starts the recording the config sets, which records until the server closes, or
// StopRecording ends it.
func (app *Server) onStart() error {
	if err := app.load(); err != nil {
		return err
	}

	return app.startConfiguredRecording()
}

// load loads the mock files, with the mocks of the admin API's persistent store, if any, then runs the loaders of the
// config.
func (app *Server) load() error {
	var err error
	if app.adminStore == nil {
		err = app.files.load(app, nil)
	} else {
		// A mock of the persistent store gives way to a mock file's mock of its id. Both swap in one step, and stay as
		// they were when a file has problems, or the store can't be read or its mocks served.
		err = app.adminStore.Reload(context.Background(), func(swap vlatte.Swap[*Mock]) error {
			return app.files.load(app, swap)
		})
	}

	if err != nil {
		return err
	}

	for _, loader := range app.loaders {
		if err := loader.Load(app); err != nil {
			return err
		}
	}

	return nil
}

// logOutput returns where the server writes its logs and its debug output: Config.LogOutput, or the standard output.
func (app *Server) logOutput() io.Writer {
	if app != nil && app.config != nil && app.config.LogOutput != nil {
		return app.config.LogOutput
	}

	return os.Stdout
}

// getLog returns the logger of conf, or builds one that writes to its log output, and to tee unless it is nil.
func (app *Server) getLog(conf *Config, tee io.Writer) *zerolog.Logger {
	if conf.Logger != nil {
		return conf.Logger
	}

	var output io.Writer
	if conf.LogPretty {
		output = zerolog.ConsoleWriter{Out: app.logOutput(), TimeFormat: time.RFC3339}
	} else {
		output = app.logOutput()
	}

	if tee != nil {
		output = zerolog.MultiLevelWriter(output, tee)
	}

	c := zerolog.New(output).Level(zerolog.Level(conf.LogLevel)).With().Timestamp()
	if conf.Name != "" {
		c = c.Str("server", conf.Name)
	}

	log := c.Logger()
	return &log
}
