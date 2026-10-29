package mockhttp

import (
	"cmp"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

	// OutboundAllow and OutboundDeny limit the addresses the server connects to, for its proxy replies, the recording
	// target, the forward proxy and its tunnels, and webhooks: an address in OutboundDeny is refused, and so is one
	// outside OutboundAllow when it isn't empty. Deny wins, and either one refuses the unspecified address, 0.0.0.0 or
	// ::, which reaches this host. They check the address after DNS resolution, so a host name that resolves into a
	// refused range is refused, and through a proxy on the way, Via or the proxy of the environment, they check the
	// proxy's. An IPv4 range doesn't hold IPv6 addresses: deny ::1/128 with 127.0.0.0/8. A refused connection fails
	// with [vlatte.ErrOutboundRefused]. Empty, the default, refuses nothing. A transport of the caller's, such as
	// [Config.Transport], dials on its own, and they don't apply.
	OutboundAllow []netip.Prefix
	OutboundDeny  []netip.Prefix

	// proxyAll is the proxy reply of the setting proxy_all, which answers the requests no mock matches, without a
	// mock: see Server.proxyAll. Code mocks ProxyAll itself.
	proxyAll *proxyDef
}

// Apply implements Option: it sets every field of conf to c's, its zero values included. [Setup] sets only what its
// methods set, so its options compose.
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
	conf.MockFileHandlers = c.MockFileHandlers
	conf.Transport = c.Transport
	conf.OutboundAllow = c.OutboundAllow
	conf.OutboundDeny = c.OutboundDeny
	conf.HeaderNamesToRedact = c.HeaderNamesToRedact
	conf.proxyAll = c.proxyAll

	return nil
}

// Path returns where the file or pattern p of the server is: a leading [WorkDirVar] is the server's work directory,
// and a relative path starts at [Config.RootDir]. Path(WorkDirVar) is the work directory itself.
func (c *Config) Path(p string) string {
	if rest, ok := strings.CutPrefix(p, WorkDirVar); ok && (rest == "" || os.IsPathSeparator(rest[0])) {
		p = filepath.Join(c.workDir(), rest)
	}

	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(c.RootDir, p)
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

// ConfigBuilder builds the configuration of a Server with a fluent API. Each method sets one setting, and Apply sets
// only those: the options passed to New compose, so a later one changes only what it sets.
type ConfigBuilder struct {
	sets []func(conf *Config) error

	// onComplete keeps the listeners of OnCompleteListener. Apply registers them in the registry of each config,
	// unless EventListeners gave a shared one: they are registered in it as soon as both are known.
	onComplete []namedListener

	// shared is the registry EventListeners set, which the configs Apply builds share.
	shared *vlatte.Listener

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
		DebugFn: func(string, any) {
			// Debug logging is off unless a DebugFn is given.
		},
		GlobalEvents: &vlatte.Events{},
	}
	return c
}

// Setup initialize a new ConfigBuilder.
// Entrypoint to start a new custom configuration for HTTP mock servers.
func Setup() *ConfigBuilder {
	return &ConfigBuilder{}
}

// Name sets a name to the mock server, which its logs show. It names the folder of its files too, in DefaultWorkDir,
// unless WorkDir sets another: a path inside it, which doesn't start with _, as vlatte's own folders do.
func (cb *ConfigBuilder) Name(name string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Name = name })
}

// Addr sets a custom address for the mock HTTP server.
func (cb *ConfigBuilder) Addr(addr string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Addr = addr })
}

// Listener sets an already bound listener for the mock HTTP server to serve instead of listening on Addr.
// Start takes it: Close, or a Start that fails, closes it, so nothing listens without a server behind it.
func (cb *ConfigBuilder) Listener(l net.Listener) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Listener = l })
}

// Port sets a custom port to the mock HTTP server.
// If none is provided, a random port number will be used.
func (cb *ConfigBuilder) Port(port int) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Addr = ":" + strconv.FormatInt(int64(port), 10) })
}

// RootDir sets the directory that relative mock file patterns and recording paths start at. Absolute ones are used
// as they are.
func (cb *ConfigBuilder) RootDir(rootDir string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.RootDir = rootDir })
}

// WorkDir sets the directory of the server's own files: the mock files, recordings, admin mocks and journal of its
// defaults, whose paths start with WorkDirVar. Empty is DefaultWorkDir, or DefaultWorkDir/<name> for a named server.
// A relative dir starts at RootDir.
func (cb *ConfigBuilder) WorkDir(dir string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.WorkDir = dir })
}

// MockNotFoundStatusCode defines the status code to be used when no mock matches with an HTTP request.
func (cb *ConfigBuilder) MockNotFoundStatusCode(code int) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.RequestWasNotMatchedStatusCode = code })
}

// ErrorStatusCode defines the status code of a response vlatte makes when serving a request fails, such as a reply
// that fails. It defaults to StatusNoMatch, as no-match does: the VLatte-Error header tells them apart.
func (cb *ConfigBuilder) ErrorStatusCode(code int) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.ErrorStatusCode = code })
}

// RequestBodyParsers adds a custom list of RequestBodyParsers.
func (cb *ConfigBuilder) RequestBodyParsers(bp ...RequestBodyParser) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.RequestBodyParsers = append(conf.RequestBodyParsers, bp...) })
}

// NoBodyParsing disables request body parsing.
func (cb *ConfigBuilder) NoBodyParsing() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoBodyParsing = true })
}

// NoDebugHeaders turns off the debug headers, such as VLatte-Matched-Mock-Id, that the server adds to its answers.
// VLatte-Error stays: it tells the server's own answers from a mock's.
func (cb *ConfigBuilder) NoDebugHeaders() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoDebugHeaders = true })
}

// NoNearMisses turns off the report of the mocks closest to a request no mock matched, which the answer to it lists.
// The report runs no matcher twice: it runs those matching never reached, such as the action of a match.Peek after
// the part a mock differs in.
func (cb *ConfigBuilder) NoNearMisses() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoNearMisses = true })
}

// MaxBodyParsingLimit sets the limit of bytes that request body parsers should read.
// Zero means no limit.
func (cb *ConfigBuilder) MaxBodyParsingLimit(n int64) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.MaxBodyParsingLimit = n })
}

// Use adds middlewares that wrap the mock handler, to intercept requests. They run in the order they are added, across
// calls. The journal, the logs and the events tell the status a middleware sends, when it answers in place of a mock.
func (cb *ConfigBuilder) Use(fn ...func(handler http.Handler) http.Handler) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Middlewares = append(conf.Middlewares, fn...) })
}

// CORS configures CORS for the mock server. An answer that sends a CORS header, such as a proxied one, answers CORS by
// itself: the server's CORS headers aren't sent with it. A Handler reply writes its answer on top of the server's CORS
// headers: it should Set its CORS headers, or Del the server's.
func (cb *ConfigBuilder) CORS(options ...CORSConfigurer) *ConfigBuilder {
	opts := DefaultCORSConfig
	for _, option := range options {
		option.Apply(&opts)
	}

	return cb.set(func(conf *Config) { conf.CORS = &opts })
}

// BackendFactory configures a custom BackendFactory used to build the underlying
// HTTP mock Backend for a Server.
func (cb *ConfigBuilder) BackendFactory(factory BackendFactory) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.BackendFactory = factory })
}

// HandlerDecorator configures a custom HTTP handler using the default mock handler. The journal, the logs and the
// events tell the status it sends, when it answers in place of a mock, such as on a timeout.
func (cb *ConfigBuilder) HandlerDecorator(fn func(handler http.Handler) http.Handler) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.HandlerDecorator = fn })
}

// Logger sets a custom zerolog.Logger.
func (cb *ConfigBuilder) Logger(l *zerolog.Logger) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Logger = l })
}

// LogOutput sets where the server writes its logs, its debug output and the requests echo replies log, in place of
// the standard output, such as for a program that keeps the standard output for something else. A Logger writes
// where it was told to.
func (cb *ConfigBuilder) LogOutput(w io.Writer) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogOutput = w })
}

// Silent turns the server's logs off: it sets the log level to [vlatte.LogLevelDisabled]. Echo replies set with
// [EchoReply.Log] still write requests to the log output.
func (cb *ConfigBuilder) Silent() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogLevel = vlatte.LogLevelDisabled })
}

// LogVerbosity sets the verbosity of informative logs.
func (cb *ConfigBuilder) LogVerbosity(l vlatte.LogVerbosity) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogVerbosity = l })
}

// LogLevel sets the level of the zerolog.Logger.
func (cb *ConfigBuilder) LogLevel(l vlatte.LogLevel) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogLevel = l })
}

// LogPretty enable/disable pretty logging.
func (cb *ConfigBuilder) LogPretty(v bool) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogPretty = v })
}

// LogBodyMaxSize sets the max size of the response body to be logged.
func (cb *ConfigBuilder) LogBodyMaxSize(size int64) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogBodyMaxSize = size })
}

// UseDescriptiveLogger enable/disable the use of a more descriptive logger.
func (cb *ConfigBuilder) UseDescriptiveLogger() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Debug = true })
}

// Colors enable/disable use of ANSI colors in logs.
func (cb *ConfigBuilder) Colors(v bool) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Colors = v })
}

// RedactHeader hides the values of the given HTTP headers in logs and recordings. A recording writes <redacted> for
// the values of a response header, and matches a request header that RecordRequestHeaders names by its presence.
func (cb *ConfigBuilder) RedactHeader(names ...string) *ConfigBuilder {
	return cb.set(func(conf *Config) {
		redact := maps.Clone(conf.HeaderNamesToRedact)
		if redact == nil {
			redact = make(map[string]struct{}, len(names))
		}

		for _, name := range names {
			redact[strings.ToLower(name)] = struct{}{}
		}

		conf.HeaderNamesToRedact = redact
	})
}

// Parameters sets a custom reply parameters store.
func (cb *ConfigBuilder) Parameters(params vlatte.KV) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Parameters = params })
}

// MockFilePatterns sets custom Glob patterns to load mock from the file system, which may start with WorkDirVar.
// Defaults to ConfigMockFilePattern.
func (cb *ConfigBuilder) MockFilePatterns(patterns ...string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.MockFileSearchPatterns = patterns })
}

// Loader configures an additional Loader.
func (cb *ConfigBuilder) Loader(loader Loader) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Loaders = append(conf.Loaders, loader) })
}

// Proxy configures the mock server as a proxy server.
// Non-Matched requests will be routed based on the proxy configuration.
func (cb *ConfigBuilder) Proxy(options ...ProxyConfigurer) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		proxyConfig := _defaultProxyConfig
		for _, option := range options {
			if err := option.Apply(&proxyConfig); err != nil {
				return err
			}
		}

		conf.Proxy = &proxyConfig

		return nil
	})

	return cb
}

// Record configures recording.
func (cb *ConfigBuilder) Record(options ...RecordConfigurer) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		recordConfig := defaultRecordConfig()
		for _, option := range options {
			if err := option.Apply(recordConfig); err != nil {
				return err
			}
		}

		conf.Record = recordConfig

		return nil
	})

	return cb
}

// MockFileHandlers sets MockFileHandler implementations.
func (cb *ConfigBuilder) MockFileHandlers(handlers ...MockFileHandler) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.MockFileHandlers = append(conf.MockFileHandlers, handlers...) })
}

// Transport sets the transport of what the server sends. See Config.Transport.
func (cb *ConfigBuilder) Transport(rt http.RoundTripper) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Transport = rt })
}

// OutboundAllow adds the ranges the server may connect to: once set, it refuses every other address. See
// [Config.OutboundAllow].
func (cb *ConfigBuilder) OutboundAllow(prefixes ...netip.Prefix) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.OutboundAllow = append(conf.OutboundAllow, prefixes...) })
}

// OutboundDeny adds ranges the server refuses to connect to, such as 169.254.0.0/16. See [Config.OutboundAllow].
func (cb *ConfigBuilder) OutboundDeny(prefixes ...netip.Prefix) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.OutboundDeny = append(conf.OutboundDeny, prefixes...) })
}

// TLSConfig defines custom TLS configurations.
func (cb *ConfigBuilder) TLSConfig(c *tls.Config) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TLSConfig = c })
}

// TLSCertKeyPair sets a custom public/private key pair.
// If none is provided, default values will be used when starting the server with Server.StartTLS or Server.MustStartTLS.
// If TLSConfig is set, this option will be ignored.
func (cb *ConfigBuilder) TLSCertKeyPair(certFile, keyFile string) *ConfigBuilder {
	return cb.TLSMutual(certFile, keyFile, "")
}

// TLSMutual defines the set of root certificate authorities that the server will use.
// If TLSConfig is set, this option will be ignored.
func (cb *ConfigBuilder) TLSMutual(certFile, keyFile, clientCert string) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		if certFile == "" && keyFile == "" {
			return nil
		}

		if err := applyTLS(conf, certFile, keyFile, clientCert); err != nil {
			return fmt.Errorf("config: failed to build TLS config: %w", err)
		}

		return nil
	})

	return cb
}

// OnCompleteListener registers an on-complete event listener by name, for the servers built from the builder from then
// on: a server built before doesn't get it. Without EventListeners, it goes in the server's registry, or in the one an
// earlier option set. A name registered twice fails the server's New, with vlatte.ErrEventAlreadyRegistered.
func (cb *ConfigBuilder) OnCompleteListener(name string, listener vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
	cb.onComplete = append(cb.onComplete, namedListener{name: name, listener: listener})
	if cb.shared != nil {
		cb.register(name, listener)
	}

	return cb
}

// GlobalEvents sets the events that run for every mock.
func (cb *ConfigBuilder) GlobalEvents(events *vlatte.Events) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.GlobalEvents = events })
}

// EventListeners sets the registry of named event listeners, such as one several servers share. The listeners
// registered with OnCompleteListener, before or after, are registered in it too, so none is dropped. Nil gives each
// server a registry of its own again.
func (cb *ConfigBuilder) EventListeners(listeners *vlatte.Listener) *ConfigBuilder {
	cb.shared = listeners
	if cb.shared != nil {
		for _, l := range cb.onComplete {
			cb.register(l.name, l.listener)
		}
	}

	return cb.set(func(conf *Config) {
		if listeners == nil {
			conf.Listeners = vlatte.NewListener()
			return
		}

		conf.Listeners = listeners
	})
}

// Matchers sets the registry that builds the matcher lists of the server's mock files, such as one that holds
// custom matchers: [matchbuild.WithMatcher]. Servers can share a registry. Nil builds the built-in matchers only.
func (cb *ConfigBuilder) Matchers(r *matchbuild.Registry) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Matchers = r })
}

// TLSCertificates sets the server TLS certificates.
func (cb *ConfigBuilder) TLSCertificates(certs []tls.Certificate) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TLSCertificates = certs })
}

// TLSClientCAs sets the certificate authorities used to verify client certificates (mutual TLS).
func (cb *ConfigBuilder) TLSClientCAs(pool *x509.CertPool) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TLSClientCAs = pool })
}

// DebugFn sets the debug function.
func (cb *ConfigBuilder) DebugFn(dfn DebugFn) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.DebugFn = dfn })
}

// ConfigSources sets where settings are read from after code, lowest precedence first,
// replacing DefaultConfigSources. With no arguments, only code and defaults apply.
// A Config applied later resets it, since it sets every field.
func (cb *ConfigBuilder) ConfigSources(sources ...ConfigSource) *ConfigBuilder {
	if sources == nil {
		sources = []ConfigSource{}
	}

	return cb.set(func(conf *Config) { conf.ConfigSources = sources })
}

// Admin serves the admin API api, such as admin.MustNew(admin.Addr("127.0.0.1:8081")), on an HTTP server of its own.
// Server.AdminURL returns where it listens.
func (cb *ConfigBuilder) Admin(api AdminAPI) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Admin = api })
}

// Journal keeps a journal of the requests the server serves, in dir, across restarts: see JournalConfig. Empty keeps
// it in a temporary directory, which Close removes, as a test wants: a named one keeps the requests of earlier runs.
func (cb *ConfigBuilder) Journal(dir string) *ConfigBuilder {
	return cb.set(func(conf *Config) { config.Section(&conf.Journal, defaultJournalConfig()).Dir = dir })
}

// AdminStore keeps the mocks the admin API adds in the persistent store store makes, such as FileStore(dir): each
// change the admin API makes reaches it at once, and the server serves what it holds when it starts and on each
// Reload, so changes made to it directly take effect too. Without one, those mocks stay in memory, as long as the
// server runs. It needs an admin API: see Admin.
func (cb *ConfigBuilder) AdminStore(store AdminStore) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.AdminStore = store })
}

// TemplateEngine sets the engine that compiles every template of the server's mocks: reply bodies, headers and body
// file names, multipart parts, and the parameters of events such as webhooks. Without one, the server uses a
// vlatte.GoTemplateEngine. To give Go templates more functions, set one made with vlatte.WithTemplateFuncs.
func (cb *ConfigBuilder) TemplateEngine(e vlatte.TemplateEngine) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TemplateEngine = e })
}

// TemplateEnv allows the templates of the server's default engine to read the environment variables names with env.
// See Config.TemplateEnv.
func (cb *ConfigBuilder) TemplateEnv(names ...string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TemplateEnv = names })
}

// TemplateDataProviders sets template data providers.
func (cb *ConfigBuilder) TemplateDataProviders(sdp []TemplateDataProvider) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TemplateDataProviders = sdp })
}

// Apply implements Option: it sets, in order, what the methods of cb set, and leaves the rest of conf as it is.
func (cb *ConfigBuilder) Apply(conf *Config) error {
	if cb.err != nil {
		return cb.err
	}

	for _, set := range cb.sets {
		if err := set(conf); err != nil {
			return err
		}
	}

	// Without a shared registry, the listeners go in the config's own: a listener registered later doesn't reach the
	// servers built before.
	if cb.shared == nil && len(cb.onComplete) > 0 {
		if conf.Listeners == nil {
			conf.Listeners = vlatte.NewListener()
		}

		return registerAll(conf.Listeners, cb.onComplete)
	}

	return nil
}

// set adds fn to the settings Apply sets.
func (cb *ConfigBuilder) set(fn func(conf *Config)) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		fn(conf)
		return nil
	})

	return cb
}

// register adds listener to the shared registry. A name the registry has already joins its error to the one Apply
// returns.
func (cb *ConfigBuilder) register(name string, listener vlatte.EventListener[vlatte.OnComplete]) {
	cb.err = errors.Join(cb.err, registerAll(cb.shared, []namedListener{{name: name, listener: listener}}))
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
		cp, err := config.CertPool(clientCertFile)
		if err != nil {
			return err
		}

		c.TLSClientCAs = cp
	}

	c.TLSCertificates = []tls.Certificate{cert}

	return nil
}

// workDir returns the directory of the server's own files: WorkDir, or DefaultWorkDir, the folder of the server's name
// in it for a named server.
func (c *Config) workDir() string {
	return cmp.Or(c.WorkDir, filepath.Join(DefaultWorkDir, c.Name))
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
