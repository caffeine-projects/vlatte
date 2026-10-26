package mockhttp

import (
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

const (
	// DefaultWorkDir is the work directory of a server without a name, and holds a named server's: see Config.WorkDir.
	DefaultWorkDir = "testdata/vlatte"

	// WorkDirVar, at the start of a path, stands for the server's work directory, as in "{workdir}/_mocks".
	WorkDirVar = "{workdir}"
)

// Defaults
var (
	// ConfigMockFilePattern is the default filename glob pattern to search for local mock files, in the server's work
	// directory: the recordings of the default record directory too.
	ConfigMockFilePattern = append([]string{WorkDirVar + "/_mocks/*mock.yaml", WorkDirVar + "/_mocks/*mock.yml"}, recordedMockFiles(_defaultRecordDir)...)
)

// errTemplateEnvWithEngine says a server has both a TemplateEngine and template.env, which configures the engine the
// server makes without one.
var errTemplateEnvWithEngine = errors.New("config: template.env configures the default template engine, and the " +
	"server has a TemplateEngine: allow the variables on it with vlatte.WithEnv")

// Config holds the configuration of an HTTP mock Server.
type Config struct {
	// Common fields
	Name                  string
	RootDir               string
	WorkDir               string
	TLSConfig             *tls.Config
	TLSCertificates       []tls.Certificate
	TLSClientCAs          *x509.CertPool
	Parameters            vlatte.KV
	Logger                *zerolog.Logger
	LogOutput             io.Writer
	LogVerbosity          vlatte.LogVerbosity
	LogLevel              vlatte.LogLevel
	LogPretty             bool
	LogBodyMaxSize        int64
	Debug                 bool
	Colors                bool
	Listeners             *vlatte.Listener
	GlobalEvents          *vlatte.Events
	TemplateDataProviders []TemplateDataProvider
	DebugFn               DebugFn

	// Matchers builds the matcher lists of mock files, and of the admin API's request queries: the built-in matchers
	// and custom ones. Nil builds the built-in matchers only.
	Matchers *matchbuild.Registry

	// TemplateEngine compiles every template of the server's mocks. Nil uses a vlatte.GoTemplateEngine.
	TemplateEngine vlatte.TemplateEngine

	// TemplateEnv names the environment variables templates may read with env, from the setting template.env. It
	// configures the vlatte.GoTemplateEngine the server makes without a TemplateEngine, so setting both is an error:
	// allow them on that engine with vlatte.WithEnv.
	TemplateEnv []string

	// ConfigSources lists where settings are read from after code, lowest precedence first.
	// Nil reads DefaultConfigSources(nil): the config file, then VLATTE_* environment variables.
	// An empty, non-nil slice reads nothing, so only code and defaults apply.
	ConfigSources []ConfigSource

	// HTTP-specific fields
	Addr                           string
	Listener                       net.Listener // in place of listening on Addr: see ConfigBuilder.Listener
	UseHTTP2                       bool
	RequestWasNotMatchedStatusCode int
	ErrorStatusCode                int
	RequestBodyParsers             []RequestBodyParser
	MaxBodyParsingLimit            int64
	NoBodyParsing                  bool
	NoDebugHeaders                 bool
	NoNearMisses                   bool
	Middlewares                    []func(http.Handler) http.Handler
	CORS                           *CORSConfig
	BackendFactory                 BackendFactory
	HandlerDecorator               func(handler http.Handler) http.Handler
	MockFileSearchPatterns         []string
	Loaders                        []Loader
	Proxy                          *ProxyConfig
	Record                         *RecordConfig
	Admin                          AdminAPI
	AdminStore                     AdminStore
	Journal                        *JournalConfig
	MockFileHandlers               []MockFileHandler
	HeaderNamesToRedact            map[string]struct{}
	UseHTTPS                       bool

	// Transport sends what the server sends: the requests of proxy replies, of the recording target, of the forward
	// proxy and of webhooks. Each takes its own transport first, when it has one: ProxyReply.Transport,
	// RecordConfig.Transport, ProxyConfig.Transport or webhook.WithTransport. For upstreams with different needs,
	// give each its own, or pass one that picks a transport by req.URL.Host. It is the caller's: the server never
	// closes it.
	//
	// Nil leaves each to a transport it makes, and closes with the server. Only the transports it makes for proxy
	// replies, the recording target and the forward proxy follow its TLS client certificates and CAs: the one of
	// webhooks has the settings of http.DefaultTransport. A setting that only shapes those, and that a transport of the
	// caller's would ignore, fails instead: a proxy reply's SSLVerify or SkipSSLVerify fails its mock,
	// RecordConfig.SkipSSLVerify fails the recording, and the forward proxy's Via or SSLVerify fails New. The forward
	// proxy's CONNECT tunnels don't go through a transport: with one of the caller's, they dial their host, and else
	// they go through the proxy the transport the server makes picks, Via or the proxy of the environment.
	Transport http.RoundTripper

	// WebhookTimeout is how long a webhook waits for its answer, from the setting webhook.timeout, unless the webhook
	// has a timeout of its own, as webhook.WithTimeout gives. 0 uses the webhook's default, 10s.
	WebhookTimeout time.Duration

	// proxyAll is the proxy reply of the setting proxy_all, which answers the requests no mock matches, without a
	// mock: see Server.proxyAll. Code mocks ProxyAll itself.
	proxyAll *proxyDef
}

// Apply copies the current Config struct values to the given Config parameter.
func (c *Config) Apply(conf *Config) error {
	conf.Name = c.Name
	conf.RootDir = c.RootDir
	conf.WorkDir = c.WorkDir
	conf.TLSConfig = c.TLSConfig
	conf.TLSCertificates = c.TLSCertificates
	conf.TLSClientCAs = c.TLSClientCAs
	conf.Parameters = c.Parameters
	conf.Logger = c.Logger
	conf.LogOutput = c.LogOutput
	conf.LogVerbosity = c.LogVerbosity
	conf.LogLevel = c.LogLevel
	conf.LogPretty = c.LogPretty
	conf.LogBodyMaxSize = c.LogBodyMaxSize
	conf.Debug = c.Debug
	conf.Colors = c.Colors
	conf.Listeners = c.Listeners
	conf.Matchers = c.Matchers
	conf.GlobalEvents = c.GlobalEvents
	conf.TemplateEngine = c.TemplateEngine
	conf.TemplateEnv = c.TemplateEnv
	conf.TemplateDataProviders = c.TemplateDataProviders
	conf.DebugFn = c.DebugFn
	conf.ConfigSources = c.ConfigSources
	conf.Addr = c.Addr
	conf.Listener = c.Listener
	conf.UseHTTP2 = c.UseHTTP2
	conf.RequestWasNotMatchedStatusCode = c.RequestWasNotMatchedStatusCode
	conf.ErrorStatusCode = c.ErrorStatusCode
	conf.RequestBodyParsers = c.RequestBodyParsers
	conf.MaxBodyParsingLimit = c.MaxBodyParsingLimit
	conf.NoBodyParsing = c.NoBodyParsing
	conf.NoDebugHeaders = c.NoDebugHeaders
	conf.NoNearMisses = c.NoNearMisses
	conf.Journal = c.Journal
	conf.Middlewares = c.Middlewares
	conf.CORS = c.CORS
	conf.BackendFactory = c.BackendFactory
	conf.HandlerDecorator = c.HandlerDecorator
	conf.MockFileSearchPatterns = c.MockFileSearchPatterns
	conf.Loaders = c.Loaders
	conf.Proxy = c.Proxy
	conf.Record = c.Record
	conf.Admin = c.Admin
	conf.AdminStore = c.AdminStore
	conf.UseHTTPS = c.UseHTTPS
	conf.WebhookTimeout = c.WebhookTimeout
	conf.MockFileHandlers = c.MockFileHandlers
	conf.Transport = c.Transport
	conf.HeaderNamesToRedact = c.HeaderNamesToRedact
	conf.proxyAll = c.proxyAll

	return nil
}

// AdminAPI is an admin API the server opts into: a RESTful HTTP server of its own, apart from the mocks, that lists,
// adds and changes them while the server runs. Make one with [github.com/caffeine-projects/vlatte/admin.New]. A nil
// AdminAPI serves none.
type AdminAPI = adminapi.API

// JournalConfig configures the journal of a server: the requests its mocks get, of every outcome, which tests and the
// admin API verify and search, while the server runs: Close closes it. The requests the server answers before its
// mocks get them, such as CORS preflights, and those a middleware answers, aren't in it. A nil *JournalConfig keeps
// none.
type JournalConfig struct {
	// Dir is where the journal keeps its files, across restarts: a server that starts again finds the requests it
	// served before. Relative paths start at RootDir, and WorkDirVar at the server's work directory, as in
	// "{workdir}/_journal". Empty keeps them in a temporary directory, which Close removes.
	Dir string

	// MaxEntries is how many requests the journal keeps, the newest. 0 keeps them all.
	MaxEntries int

	// MaxBodySize is how many bytes of a request's body the journal keeps. 0 keeps whole bodies.
	MaxBodySize int64
}

func defaultJournalConfig() JournalConfig {
	return JournalConfig{MaxEntries: journal.DefaultMaxEntries, MaxBodySize: journal.DefaultMaxBodySize}
}

// AdminStore makes the persistent store of the mocks the admin API adds, for the server app. The store keeps their
// descriptions, by ID, such as in files or a database: EncodeMock describes a mock, and DecodeMock builds it back.
// The mocks of mock files and code never reach it.
type AdminStore func(app *Server) (vlatte.Store[*Mock], error)

// DebugFn is a function for debug logging.
type DebugFn func(string, any)

// ConfigBuilder lets users create a Config using a fluent API.
type ConfigBuilder struct {
	conf                   *Config
	recorderConf           []RecordConfigurer
	proxyConf              []ProxyConfigurer
	tlsCertificateFs       string
	tlsKeyFs               string
	tlsClientCertificateFs string

	// onComplete keeps the listeners of OnCompleteListener. Apply registers them in a registry of each config's own,
	// unless EventListeners gave a shared one: they are registered in it as soon as both are known.
	onComplete []namedListener

	// shared is set by EventListeners with a registry: the configs Apply builds share it.
	shared bool

	// err fails Apply, with every listener the shared registry couldn't register.
	err error
}

// namedListener is an on-complete event listener and the name it is registered under.
type namedListener struct {
	name     string
	listener vlatte.EventListener[vlatte.OnComplete]
}

func defaultConfig() *Config {
	c := &Config{
		RequestWasNotMatchedStatusCode: StatusNoMatch,
		ErrorStatusCode:                StatusNoMatch,
		MockFileSearchPatterns:         ConfigMockFilePattern,
		RequestBodyParsers:             make([]RequestBodyParser, 0),
		Middlewares:                    make([]func(http.Handler) http.Handler, 0),
		Loaders:                        make([]Loader, 0),
		MockFileHandlers:               make([]MockFileHandler, 0),
		HeaderNamesToRedact:            make(map[string]struct{}),
		UseHTTP2:                       true,
		Listeners:                      vlatte.NewListener(),
		LogPretty:                      true,
		LogLevel:                       vlatte.LogLevelInfo,
		LogVerbosity:                   vlatte.LogHeader,
		Colors:                         true,
		DebugFn:                        func(string, any) {},
		GlobalEvents:                   &vlatte.Events{},
	}
	return c
}

// Setup initialize a new ConfigBuilder.
// Entrypoint to start a new custom configuration for HTTP mock servers.
func Setup() *ConfigBuilder {
	conf := defaultConfig()
	return &ConfigBuilder{
		conf: conf,
	}
}

// Name sets a name to the mock server, which its logs show. It names the folder of its files too, in DefaultWorkDir,
// unless WorkDir sets another: a path inside it, which doesn't start with _, as vlatte's own folders do.
func (cb *ConfigBuilder) Name(name string) *ConfigBuilder {
	cb.conf.Name = name
	return cb
}

// Addr sets a custom address for the mock HTTP server.
func (cb *ConfigBuilder) Addr(addr string) *ConfigBuilder {
	cb.conf.Addr = addr
	return cb
}

// Listener sets an already bound listener for the mock HTTP server to serve instead of listening on Addr.
// Start takes it: Close, or a Start that fails, closes it, so nothing listens without a server behind it.
func (cb *ConfigBuilder) Listener(l net.Listener) *ConfigBuilder {
	cb.conf.Listener = l
	return cb
}

// Port sets a custom port to the mock HTTP server.
// If none is provided, a random port number will be used.
func (cb *ConfigBuilder) Port(port int) *ConfigBuilder {
	cb.conf.Addr = ":" + strconv.FormatInt(int64(port), 10)
	return cb
}

// RootDir sets the directory that relative mock file patterns and recording paths start at. Absolute ones are used
// as they are.
func (cb *ConfigBuilder) RootDir(rootDir string) *ConfigBuilder {
	cb.conf.RootDir = rootDir
	return cb
}

// WorkDir sets the directory of the server's own files: the mock files, recordings, admin mocks and journal of its
// defaults, whose paths start with WorkDirVar. Empty is DefaultWorkDir, or DefaultWorkDir/<name> for a named server.
// A relative dir starts at RootDir.
func (cb *ConfigBuilder) WorkDir(dir string) *ConfigBuilder {
	cb.conf.WorkDir = dir
	return cb
}

// MockNotFoundStatusCode defines the status code to be used when no mock matches with an HTTP request.
func (cb *ConfigBuilder) MockNotFoundStatusCode(code int) *ConfigBuilder {
	cb.conf.RequestWasNotMatchedStatusCode = code
	return cb
}

// ErrorStatusCode defines the status code of a response vlatte makes when serving a request fails, such as a reply
// that fails. It defaults to StatusNoMatch, as no-match does: the VLatte-Error header tells them apart.
func (cb *ConfigBuilder) ErrorStatusCode(code int) *ConfigBuilder {
	cb.conf.ErrorStatusCode = code
	return cb
}

// RequestBodyParsers adds a custom list of RequestBodyParsers.
func (cb *ConfigBuilder) RequestBodyParsers(bp ...RequestBodyParser) *ConfigBuilder {
	cb.conf.RequestBodyParsers = append(cb.conf.RequestBodyParsers, bp...)
	return cb
}

// NoBodyParsing disables request body parsing.
func (cb *ConfigBuilder) NoBodyParsing() *ConfigBuilder {
	cb.conf.NoBodyParsing = true
	return cb
}

// NoDebugHeaders turns off the debug headers, such as VLatte-Matched-Mock-Id, that the server adds to its answers.
// VLatte-Error stays: it tells the server's own answers from a mock's.
func (cb *ConfigBuilder) NoDebugHeaders() *ConfigBuilder {
	cb.conf.NoDebugHeaders = true
	return cb
}

// NoNearMisses turns off the report of the mocks closest to a request no mock matched, which the answer to it lists.
// The report runs no matcher twice: it runs those matching never reached, such as the action of a match.Peek after
// the part a mock differs in.
func (cb *ConfigBuilder) NoNearMisses() *ConfigBuilder {
	cb.conf.NoNearMisses = true
	return cb
}

// MaxBodyParsingLimit sets the limit of bytes that request body parsers should read.
// Zero means no limit.
func (cb *ConfigBuilder) MaxBodyParsingLimit(n int64) *ConfigBuilder {
	cb.conf.MaxBodyParsingLimit = n
	return cb
}

// Use adds middlewares that wrap the mock handler, to intercept requests. They run in the order they are added, across
// calls. The journal, the logs and the events tell the status a middleware sends, when it answers in place of a mock.
func (cb *ConfigBuilder) Use(fn ...func(handler http.Handler) http.Handler) *ConfigBuilder {
	cb.conf.Middlewares = append(cb.conf.Middlewares, fn...)
	return cb
}

// CORS configures CORS for the mock server. An answer that sends a CORS header, such as a proxied one, answers CORS by
// itself: the server's CORS headers aren't sent with it. A Handler reply writes its answer on top of the server's CORS
// headers: it should Set its CORS headers, or Del the server's.
func (cb *ConfigBuilder) CORS(options ...CORSConfigurer) *ConfigBuilder {
	opts := DefaultCORSConfig
	for _, option := range options {
		option.Apply(&opts)
	}

	cb.conf.CORS = &opts

	return cb
}

// BackendFactory configures a custom BackendFactory used to build the underlying
// HTTP mock Backend for a Server.
func (cb *ConfigBuilder) BackendFactory(factory BackendFactory) *ConfigBuilder {
	cb.conf.BackendFactory = factory
	return cb
}

// HandlerDecorator configures a custom HTTP handler using the default mock handler. The journal, the logs and the
// events tell the status it sends, when it answers in place of a mock, such as on a timeout.
func (cb *ConfigBuilder) HandlerDecorator(fn func(handler http.Handler) http.Handler) *ConfigBuilder {
	cb.conf.HandlerDecorator = fn
	return cb
}

// Logger sets a custom zerolog.Logger.
func (cb *ConfigBuilder) Logger(l *zerolog.Logger) *ConfigBuilder {
	cb.conf.Logger = l
	return cb
}

// LogOutput sets where the server writes its logs, its debug output and the requests echo replies log, in place of
// the standard output, such as for a program that keeps the standard output for something else. A Logger writes
// where it was told to.
func (cb *ConfigBuilder) LogOutput(w io.Writer) *ConfigBuilder {
	cb.conf.LogOutput = w
	return cb
}

func (cb *ConfigBuilder) Silent() *ConfigBuilder {
	cb.conf.LogLevel = vlatte.LogLevelDisabled
	return cb
}

// LogVerbosity sets the verbosity of informative logs.
func (cb *ConfigBuilder) LogVerbosity(l vlatte.LogVerbosity) *ConfigBuilder {
	cb.conf.LogVerbosity = l
	return cb
}

// LogLevel sets the level of the zerolog.Logger.
func (cb *ConfigBuilder) LogLevel(l vlatte.LogLevel) *ConfigBuilder {
	cb.conf.LogLevel = l
	return cb
}

// LogPretty enable/disable pretty logging.
func (cb *ConfigBuilder) LogPretty(v bool) *ConfigBuilder {
	cb.conf.LogPretty = v
	return cb
}

// LogBodyMaxSize sets the max size of the response body to be logged.
func (cb *ConfigBuilder) LogBodyMaxSize(size int64) *ConfigBuilder {
	cb.conf.LogBodyMaxSize = size
	return cb
}

// UseDescriptiveLogger enable/disable the use of a more descriptive logger.
func (cb *ConfigBuilder) UseDescriptiveLogger() *ConfigBuilder {
	cb.conf.Debug = true
	return cb
}

// Colors enable/disable use of ANSI colors in logs.
func (cb *ConfigBuilder) Colors(v bool) *ConfigBuilder {
	cb.conf.Colors = v
	return cb
}

// RedactHeader hides the values of the given HTTP headers in logs and recordings. A recording writes <redacted> for
// the values of a response header, and matches a request header that RecordRequestHeaders names by its presence.
func (cb *ConfigBuilder) RedactHeader(names ...string) *ConfigBuilder {
	for _, name := range names {
		cb.conf.HeaderNamesToRedact[strings.ToLower(name)] = struct{}{}
	}

	return cb
}

// Parameters sets a custom reply parameters store.
func (cb *ConfigBuilder) Parameters(params vlatte.KV) *ConfigBuilder {
	cb.conf.Parameters = params
	return cb
}

// MockFilePatterns sets custom Glob patterns to load mock from the file system, which may start with WorkDirVar.
// Defaults to ConfigMockFilePattern.
func (cb *ConfigBuilder) MockFilePatterns(patterns ...string) *ConfigBuilder {
	cb.conf.MockFileSearchPatterns = patterns

	return cb
}

// Loader configures an additional Loader.
func (cb *ConfigBuilder) Loader(loader Loader) *ConfigBuilder {
	cb.conf.Loaders = append(cb.conf.Loaders, loader)
	return cb
}

// Proxy configures the mock server as a proxy server.
// Non-Matched requests will be routed based on the proxy configuration.
func (cb *ConfigBuilder) Proxy(options ...ProxyConfigurer) *ConfigBuilder {
	if len(options) == 0 {
		c := _defaultProxyConfig
		cb.proxyConf = []ProxyConfigurer{&c}

		return cb
	}

	cb.proxyConf = options

	return cb
}

// Record configures recording.
func (cb *ConfigBuilder) Record(options ...RecordConfigurer) *ConfigBuilder {
	if len(options) == 0 {
		cb.recorderConf = []RecordConfigurer{defaultRecordConfig()}
		return cb
	}

	cb.recorderConf = options

	return cb
}

// MockFileHandlers sets MockFileHandler implementations.
func (cb *ConfigBuilder) MockFileHandlers(handlers ...MockFileHandler) *ConfigBuilder {
	cb.conf.MockFileHandlers = append(cb.conf.MockFileHandlers, handlers...)
	return cb
}

// Transport sets the transport of what the server sends. See Config.Transport.
func (cb *ConfigBuilder) Transport(rt http.RoundTripper) *ConfigBuilder {
	cb.conf.Transport = rt
	return cb
}

// TLSConfig defines custom TLS configurations.
func (cb *ConfigBuilder) TLSConfig(c *tls.Config) *ConfigBuilder {
	cb.conf.TLSConfig = c
	return cb
}

// TLSCertKeyPair sets a custom public/private key pair.
// If none is provided, default values will be used when starting the server with Server.StartTLS or Server.MustStartTLS.
// If TLSConfig is set, this option will be ignored.
func (cb *ConfigBuilder) TLSCertKeyPair(certFile, keyFile string) *ConfigBuilder {
	cb.tlsCertificateFs = certFile
	cb.tlsKeyFs = keyFile
	return cb
}

// TLSMutual defines the set of root certificate authorities that the server will use.
// If TLSConfig is set, this option will be ignored.
func (cb *ConfigBuilder) TLSMutual(certFile, keyFile, clientCert string) *ConfigBuilder {
	cb.tlsClientCertificateFs = clientCert
	cb.tlsCertificateFs = certFile
	cb.tlsKeyFs = keyFile
	return cb
}

// OnCompleteListener registers an on-complete event listener by name, for the servers built from the builder from then
// on: a server built before doesn't get it. A name registered twice fails the server's New, with
// vlatte.ErrEventAlreadyRegistered.
func (cb *ConfigBuilder) OnCompleteListener(name string, listener vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
	cb.onComplete = append(cb.onComplete, namedListener{name: name, listener: listener})
	if cb.shared {
		cb.register(name, listener)
	}

	return cb
}

// GlobalEvents sets the events that run for every mock.
func (cb *ConfigBuilder) GlobalEvents(events *vlatte.Events) *ConfigBuilder {
	cb.conf.GlobalEvents = events
	return cb
}

// EventListeners sets the registry of named event listeners, such as one several servers share. The listeners
// registered with OnCompleteListener, before or after, are registered in it too, so none is dropped. Nil gives each
// server a registry of its own again.
func (cb *ConfigBuilder) EventListeners(listeners *vlatte.Listener) *ConfigBuilder {
	cb.conf.Listeners = listeners
	cb.shared = listeners != nil

	if cb.shared {
		for _, l := range cb.onComplete {
			cb.register(l.name, l.listener)
		}
	}

	return cb
}

// Matchers sets the registry that builds the matcher lists of the server's mock files, such as one that holds
// custom matchers: [matchbuild.WithMatcher]. Servers can share a registry. Nil builds the built-in matchers only.
func (cb *ConfigBuilder) Matchers(r *matchbuild.Registry) *ConfigBuilder {
	cb.conf.Matchers = r
	return cb
}

// TLSCertificates sets the server TLS certificates.
func (cb *ConfigBuilder) TLSCertificates(certs []tls.Certificate) *ConfigBuilder {
	cb.conf.TLSCertificates = certs
	return cb
}

// TLSClientCAs sets the certificate authorities used to verify client certificates (mutual TLS).
func (cb *ConfigBuilder) TLSClientCAs(pool *x509.CertPool) *ConfigBuilder {
	cb.conf.TLSClientCAs = pool
	return cb
}

// DebugFn sets the debug function.
func (cb *ConfigBuilder) DebugFn(dfn DebugFn) *ConfigBuilder {
	cb.conf.DebugFn = dfn
	return cb
}

// ConfigSources sets where settings are read from after code, lowest precedence first,
// replacing DefaultConfigSources. With no arguments, only code and defaults apply.
// A Setup() or Config applied later resets it, since they copy every field.
func (cb *ConfigBuilder) ConfigSources(sources ...ConfigSource) *ConfigBuilder {
	if sources == nil {
		sources = []ConfigSource{}
	}

	cb.conf.ConfigSources = sources

	return cb
}

// Admin serves the admin API api, such as admin.MustNew(admin.Addr("127.0.0.1:8081")), on an HTTP server of its own.
// Server.AdminURL returns where it listens.
func (cb *ConfigBuilder) Admin(api AdminAPI) *ConfigBuilder {
	cb.conf.Admin = api
	return cb
}

// Journal keeps a journal of the requests the server serves, in dir, across restarts: see JournalConfig. Empty keeps
// it in a temporary directory, which Close removes, as a test wants: a named one keeps the requests of earlier runs.
func (cb *ConfigBuilder) Journal(dir string) *ConfigBuilder {
	config.Section(&cb.conf.Journal, defaultJournalConfig()).Dir = dir
	return cb
}

// AdminStore keeps the mocks the admin API adds in the persistent store store makes, such as FileStore(dir): each
// change the admin API makes reaches it at once, and the server serves what it holds when it starts and on each
// Reload, so changes made to it directly take effect too. Without one, those mocks stay in memory, as long as the
// server runs. It needs an admin API: see Admin.
func (cb *ConfigBuilder) AdminStore(store AdminStore) *ConfigBuilder {
	cb.conf.AdminStore = store
	return cb
}

// TemplateEngine sets the engine that compiles every template of the server's mocks: reply bodies, headers and body
// file names, multipart parts, and the parameters of events such as webhooks. Without one, the server uses a
// vlatte.GoTemplateEngine. To give Go templates more functions, set one made with vlatte.WithTemplateFuncs.
func (cb *ConfigBuilder) TemplateEngine(e vlatte.TemplateEngine) *ConfigBuilder {
	cb.conf.TemplateEngine = e
	return cb
}

// TemplateEnv allows the templates of the server's default engine to read the environment variables names with env.
// See Config.TemplateEnv.
func (cb *ConfigBuilder) TemplateEnv(names ...string) *ConfigBuilder {
	cb.conf.TemplateEnv = names
	return cb
}

// TemplateDataProviders sets template data providers.
func (cb *ConfigBuilder) TemplateDataProviders(sdp []TemplateDataProvider) *ConfigBuilder {
	cb.conf.TemplateDataProviders = sdp
	return cb
}

// Apply builds a new Config with previously configured values.
func (cb *ConfigBuilder) Apply(conf *Config) error {
	if cb.err != nil {
		return cb.err
	}

	if len(cb.recorderConf) > 0 {
		recordConfig := defaultRecordConfig()
		for _, option := range cb.recorderConf {
			err := option.Apply(recordConfig)
			if err != nil {
				return err
			}
		}

		cb.conf.Record = recordConfig
	}

	if len(cb.proxyConf) > 0 {
		proxyConfig := _defaultProxyConfig
		for _, option := range cb.proxyConf {
			err := option.Apply(&proxyConfig)
			if err != nil {
				return err
			}
		}

		cb.conf.Proxy = &proxyConfig
	}

	// Without a shared registry, each config gets one of its own: a listener registered later doesn't reach the
	// servers built before.
	listeners := cb.conf.Listeners
	if !cb.shared {
		listeners = vlatte.NewListener()
		if err := registerAll(listeners, cb.onComplete); err != nil {
			return err
		}
	}

	if len(cb.tlsCertificateFs) > 0 || len(cb.tlsKeyFs) > 0 {
		if err := applyTLS(cb.conf, cb.tlsCertificateFs, cb.tlsKeyFs, cb.tlsClientCertificateFs); err != nil {
			return fmt.Errorf("config: failed to build TLS config: %w", err)
		}
	}

	err := cb.conf.Apply(conf)
	conf.Listeners = listeners

	return err
}

// register adds listener to the shared registry. A name the registry has already joins its error to the one Apply
// returns.
func (cb *ConfigBuilder) register(name string, listener vlatte.EventListener[vlatte.OnComplete]) {
	cb.err = errors.Join(cb.err, registerAll(cb.conf.Listeners, []namedListener{{name: name, listener: listener}}))
}

// registerAll adds each listener to registry, and returns the errors of the names it has already.
func registerAll(registry *vlatte.Listener, listeners []namedListener) error {
	var errs error
	for _, l := range listeners {
		if err := registry.OnComplete.Register(l.name, l.listener); err != nil {
			errs = errors.Join(errs, fmt.Errorf("config: on_complete listener %s: %w", l.name, err))
		}
	}

	return errs
}

// applyTLS loads TLS material from the given files and configures TLS on c.
// If clientCertFile is empty, only the server certificate and key are configured.
func applyTLS(c *Config, certFile, keyFile, clientCertFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	if len(clientCertFile) > 0 {
		cp, err := loadCertPool(clientCertFile)
		if err != nil {
			return err
		}

		c.TLSClientCAs = cp
	}

	c.TLSCertificates = []tls.Certificate{cert}

	return nil
}

// loadCertPool reads the PEM certificates in file.
// A file without any is an error: an empty pool would reject every client certificate.
func loadCertPool(file string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s has no PEM certificates", file)
	}

	return pool, nil
}

// workDir returns the directory of the server's own files: WorkDir, or DefaultWorkDir, the folder of the server's name
// in it for a named server.
func (c *Config) workDir() string {
	return cmp.Or(c.WorkDir, filepath.Join(DefaultWorkDir, c.Name))
}

// path returns where the file or pattern p of the server is: a leading WorkDirVar is the server's work directory,
// and a relative path starts at RootDir.
func (c *Config) path(p string) string {
	if rest, ok := strings.CutPrefix(p, WorkDirVar); ok && (rest == "" || os.IsPathSeparator(rest[0])) {
		p = filepath.Join(c.workDir(), rest)
	}

	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(c.RootDir, p)
}

// checkName checks the name of a server that names its work directory, a folder of DefaultWorkDir.
func (c *Config) checkName() error {
	if problem := c.nameProblem(); problem != "" {
		return fmt.Errorf("config: name %q %s", c.Name, problem)
	}

	return nil
}

// nameProblem tells what is wrong with the name of a server that names its work directory, "" when nothing is: it
// stays inside DefaultWorkDir, and doesn't start with _, as vlatte's own folders do.
func (c *Config) nameProblem() string {
	name := c.Name
	if c.WorkDir != "" || name == "" {
		return ""
	}

	if !filepath.IsLocal(name) || name == "." || strings.HasPrefix(name, "_") {
		return "names the folder of the server's files, in " + DefaultWorkDir + ": use one inside it, which " +
			"doesn't start with _, or set workdir"
	}

	return ""
}
