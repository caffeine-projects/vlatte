package mockhttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mid/recover"
)

var _ vlatte.Flavor[*Mock, *Server] = (*Server)(nil)

// StatusNoMatch describes an HTTP response where no Mock was found.
//
// It uses http.StatusTeapot to reduce the chance of using the same
// expected response from the actual server being mocked.
// Basically, every request that doesn't match against a Mock, will have a response with http.StatusTeapot.
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

	data           map[string]any
	logger         *zerolog.Logger
	startOnce      sync.Once
	admin          *admin.Admin[*Mock]
	closeIdleConns []func()
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

	app.config = conf
	app.logger = app.getLog(conf)

	repository := vlatte.NewStore[*Mock]()

	parsers := make([]RequestBodyParser, 0, len(conf.RequestBodyParsers)+3)
	parsers = append(parsers, conf.RequestBodyParsers...)
	parsers = append(parsers, &plainTextParser{}, &formURLEncodedParser{}, &noopParser{})

	recovery := recover.New(func(err error) { app.logger.Error().Err(err).Msg(err.Error()) },
		conf.RequestWasNotMatchedStatusCode)

	middlewares := make([]func(handler http.Handler) http.Handler, 0)
	middlewares = append(middlewares, recovery.Recover)

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
		rec = newRecorder(app, conf.Record)
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

	eventRunner := vlatte.NewEventRunner(app.config.Listeners, app.config.GlobalEvents)
	handler := mid.Compose(middlewares...).Root(newMockHandler(app, lifecycle, conf.TemplateDataProviders, eventRunner))

	if conf.HandlerDecorator != nil {
		handler = conf.HandlerDecorator(handler)
	}

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

// URL returns the base URL string for the mock server.
// If parameter paths is provided, it will be concatenated with the server base URL.
func (app *Server) URL(paths ...string) string {
	if len(paths) == 0 {
		return app.server.Info().URL
	}

	u, err := url.JoinPath(app.server.Info().URL, paths...)
	if err != nil {
		// A library must not exit the process: panic so the caller (usually a test) reports it.
		panic(fmt.Errorf("server: building server url with path elements %s: %w", paths, err))
	}

	return u
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

// Reload reloads mocks from external sources, like the filesystem.
// Coded mocks will be kept.
func (app *Server) Reload() error {
	// app.storage.DeleteExternal()
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

// Close closes the mock server.
func (app *Server) Close() {
	err := app.server.Close()
	if err != nil {
		app.logger.Debug().Err(err).Msg("server: Close: error closing server")
	}

	if app.rec != nil {
		app.rec.stop()
	}

	app.rwMutex.RLock()
	closeIdleConns := app.closeIdleConns
	app.rwMutex.RUnlock()

	for _, closeIdle := range closeIdleConns {
		closeIdle()
	}
}

// closeIdleConnsOnClose registers fn to release idle outbound connections when the app closes.
func (app *Server) closeIdleConnsOnClose(fn func()) {
	app.rwMutex.Lock()
	defer app.rwMutex.Unlock()

	app.closeIdleConns = append(app.closeIdleConns, fn)
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

// SetData sets the data to be used as template data during mock configurations parsing.
func (app *Server) SetData(data map[string]any) {
	app.data = data
}

// Data returns the template data associated to this instance.
func (app *Server) Data() map[string]any {
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
