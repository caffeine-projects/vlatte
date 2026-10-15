package mockhttp

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mid"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mid/recovery"
)

var _ vlatte.Flavor[*Mock, *Server] = (*Server)(nil)

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

	config             *Config
	server             Backend
	storage            vlatte.Store[*Mock]
	scenarioStore      *mfeat.ScenarioStore
	requestBodyParsers []RequestBodyParser
	params             vlatte.KV
	loaders            []Loader
	rec                *recorder
	rwMutex            sync.RWMutex
	proxy              *reverseProxy

	data      map[string]any
	logger    *zerolog.Logger
	startOnce sync.Once
	admin     *admin.Admin[*Mock]

	// events runs the on_complete events of the mocks, which their responses wait for, and Close cancels them.
	events *vlatte.EventRunner
}

// New creates a new HTTP mock Server with the given configurations.
// Parameter config accepts a Config or a ConfigBuilder implementation.
// If no configuration is provided, a default one will be used.
// If no port is set, it will start the server on localhost using a random port.
// It returns an error if a configuration fails to apply or the server cannot be built.
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
		return nil, fmt.Errorf("server: error loading configuration sources\n%w", err)
	}

	if conf.DebugFn == nil {
		conf.DebugFn = func(_ string, _ any) {}
	}

	if conf.RequestWasNotMatchedStatusCode == 0 {
		conf.RequestWasNotMatchedStatusCode = StatusNoMatch
	}

	if conf.ErrorStatusCode == 0 {
		conf.ErrorStatusCode = StatusNoMatch
	}

	if conf.TemplateEngine == nil {
		// Without options, it can't fail.
		conf.TemplateEngine = vlatte.MustNewGoTemplateEngine()
	}

	if err := checkProviderKeys(conf.TemplateDataProviders); err != nil {
		return nil, err
	}

	app.config = conf
	app.logger = app.getLog(conf)

	repository := vlatte.NewStore[*Mock]()

	parsers := make([]RequestBodyParser, 0, len(conf.RequestBodyParsers)+4)
	parsers = append(parsers, conf.RequestBodyParsers...)
	parsers = append(parsers, &plainTextParser{}, &formURLEncodedParser{}, &multipartFormParser{}, &noopParser{})

	recoverer := recovery.New(func(err error) { app.logger.Error().Err(err).Msg(err.Error()) },
		conf.ErrorStatusCode, http.Header{HeaderXVlatteError: {vlatte.FailurePanic}})

	middlewares := make([]func(handler http.Handler) http.Handler, 0)
	middlewares = append(middlewares, recoverer.Recover)

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

	var rec *recorder
	if conf.Record != nil {
		var err error
		if rec, err = newRecorder(app, conf.Record); err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	}

	var p *reverseProxy
	if conf.Proxy != nil {
		p = newProxy(app.Logger(), conf)
	}

	var lifecycle mockHTTPLifecycle
	if conf.Debug {
		fd := os.Stdout.Fd()
		colors := ansi(colorsEnabled(conf.Colors, isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)))
		// On Windows consoles, colorable translates the escape codes.
		lifecycle = newBuiltInDescriptiveMockHTTPLifecycle(app, colors, colorable.NewColorable(os.Stdout))
	} else {
		lifecycle = newBuiltInMockHTTPLifecycle(app)
	}

	globalEvents, err := prepareGlobalEvents(conf)
	if err != nil {
		return nil, err
	}

	eventRunner := vlatte.NewEventRunner(app.config.Listeners, globalEvents)
	handler := mid.Compose(middlewares...).Root(newMockHandler(app, lifecycle, conf.TemplateDataProviders, eventRunner))

	if conf.HandlerDecorator != nil {
		handler = conf.HandlerDecorator(handler)
	}

	// Outermost: the decorator and the middlewares see the fragment in r.URL too.
	handler = withFragment(handler)

	factory := conf.BackendFactory

	if factory == nil {
		factory = newBackendFactory()
	}

	internalLoaders := 1
	loaders := make([]Loader, 0, len(conf.Loaders)+internalLoaders)
	loaders = append(loaders, &fileLoader{})
	loaders = append(loaders, conf.Loaders...)

	app.Base = vlatte.NewBase(app, repository)
	app.storage = repository
	app.scenarioStore = mfeat.NewScenarioStore()
	app.params = params
	app.loaders = loaders
	app.rec = rec
	app.proxy = p
	app.requestBodyParsers = parsers
	app.events = eventRunner

	app.admin = admin.New(
		repository,
		vlatte.MockBuilderFunc[*Mock](func(content []byte, ext string) (*Mock, error) {
			return buildAdminMock(app, content, ext)
		}),
	)

	server, err := factory.NewBackend(BackendConfig{App: app, Handler: handler})
	if err != nil {
		return nil, fmt.Errorf("server: backend factory failed to build the backend. %w", err)
	}

	app.server = server

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
// changing conf's: see vlatte.EventPreparer. Like a mock's events, each must name a registered listener: an event
// that names none would never run.
func prepareGlobalEvents(conf *Config) (*vlatte.Events, error) {
	if conf.GlobalEvents == nil {
		return conf.GlobalEvents, nil
	}

	events := &vlatte.Events{OnComplete: slices.Clone(conf.GlobalEvents.OnComplete)}
	for i, def := range events.OnComplete {
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
		if err := recipe.Prep(ctx, &vlatte.RecipeInput[*Mock, *Server]{Flavor: app}); err != nil {
			return fmt.Errorf("server: error applying recipe at index %d with type %T\n%w", i, recipe, err)
		}
	}

	return nil
}

// Name returns mock server name.
func (app *Server) Name() string {
	return app.config.Name
}

// Start starts the mock server.
func (app *Server) Start() error {
	// A server closed before runs events again.
	app.events.Open()

	err := app.server.Start()
	if err != nil {
		return err
	}

	return app.onStart()
}

// MustStart starts the mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStart() {
	err := app.Start()
	if err != nil {
		panic(fmt.Errorf("server: start failed. %w", err))
	}
}

// StartTLS starts TLS on a mock server.
func (app *Server) StartTLS() error {
	// A server closed before runs events again.
	app.events.Open()

	err := app.server.StartTLS()
	if err != nil {
		return err
	}

	return app.onStart()
}

// MustStartTLS starts TLS on a mock server.
// It fails immediately if any error occurs.
func (app *Server) MustStartTLS() {
	err := app.StartTLS()
	if err != nil {
		panic(fmt.Errorf("server: failed to start server with TLS. %w", err))
	}
}

// URL returns the base URL of the mock server. With paths, it joins them to it as path elements, with url.JoinPath:
// URL("/users", "1") is <base>/users/1, and "%3F" is a "?" in the path. A "?" or "#" in the last element starts the
// query or the fragment, which are appended as given, so they must already be escaped:
// URL("/users/1?active=true#top"). Go's http.Client doesn't send the fragment.
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

// Reload loads the mocks of external sources, such as mock files, again.
// Each loader replaces the mocks it added before; mocks added in code are kept.
// Mock files are swapped in one step, and stay as they were when a file has problems.
func (app *Server) Reload() error {
	return app.load()
}

// MustReload reloads mocks from external sources, like the filesystem.
// Coded mocks will be kept.
// It panics if any error occurs.
func (app *Server) MustReload() {
	err := app.Reload()
	if err != nil {
		panic(fmt.Errorf("server: error reloading mock definitions. %w", err))
	}
}

// Close closes the mock server. It cancels the on_complete events still running, such as a webhook waiting for its
// delay or its target, so the requests waiting for them end, and waits for those requests.
func (app *Server) Close() {
	// The backend waits for the requests it serves, and they wait for their events: cancel the events first.
	app.events.Close()

	err := app.server.Close()
	if err != nil {
		app.logger.Debug().Err(err).Msg("server: Close: error closing server")
	}

	if app.rec != nil {
		app.rec.stop()
	}

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
}

// IsRecording checks if mock recording is enabled.
func (app *Server) IsRecording() bool {
	return app.rec != nil && app.rec.isActive()
}

func (app *Server) StopRecording() {
	if app.rec != nil {
		app.rec.stop()
	}
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

	buf.WriteString("Mock Search Patterns: ")
	buf.WriteString(strings.Join(app.config.MockFileSearchPatterns, ", "))
	buf.WriteString("\n")

	buf.WriteString("Log: ")
	buf.WriteString(app.config.LogVerbosity.String())
	buf.WriteString("\n")

	if app.config.Proxy != nil {
		buf.WriteString("Reverse Proxy: ")
		buf.WriteString("enabled")
		buf.WriteString("\n")
	}

	if app.config.Record != nil {
		buf.WriteString("Recording: ")
		buf.WriteString(app.config.Record.SaveDir)
		buf.WriteString("\n")
	}

	buf.WriteString("\n")
	buf.WriteString("Listening: ")
	buf.WriteString(app.URL())
	buf.WriteString("\n")

	return buf.String()
}

// --
// Internals
// --

func (app *Server) onStart() (err error) {
	app.startOnce.Do(func() { err = app.load() })

	if err != nil {
		return err
	}

	if app.rec != nil {
		app.rec.start()
	}

	return nil
}

func (app *Server) load() error {
	for _, loader := range app.loaders {
		err := loader.Load(app)
		if err != nil {
			return err
		}
	}

	return nil
}

func (app *Server) getLog(conf *Config) *zerolog.Logger {
	if conf.Logger != nil {
		return conf.Logger
	}

	var output io.Writer
	if conf.LogPretty {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	} else {
		output = os.Stdout
	}

	c := zerolog.New(output).Level(zerolog.Level(conf.LogLevel)).With().Timestamp()
	if conf.Name != "" {
		c = c.Str("server", conf.Name)
	}

	log := c.Logger()
	return &log
}
