package xhttp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/template"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/flavor/xhttp/cors"
	"github.com/caffeine-projects/vlatte/vlattecore/kv"
)

// Defaults
var (
	// ConfigMockFilePattern is the default filename glob pattern to search for local mock files.
	ConfigMockFilePattern = []string{"testdata/_mocks/*mock.yaml", "testdata/_mocks/*mock.yml"}
)

// Config holds HTTPMockApp mock server configurations.
// It implements [vlatte.C] for common fields.
type Config struct {
	// Common fields (vlatte.C)
	Name                  string
	AdminPath             string
	AdminEnabled          bool
	RootDir               string
	TLSConfig             *tls.Config
	TLSCertificates       []tls.Certificate
	TLSClientCAs          *x509.CertPool
	Parameters            kv.KV
	TemplateFunctions     template.FuncMap
	Logger                *zerolog.Logger
	LogVerbosity          vlatte.LogVerbosity
	LogLevel              vlatte.LogLevel
	LogPretty             bool
	LogBodyMaxSize        int64
	Debug                 bool
	Colors                bool
	Listeners             *vlatte.Listener
	GlobalEvents          *vlatte.Events
	Template              vlatte.Template
	TemplateDataProviders []vlatte.TemplateDataProvider
	DebugFn               vlatte.DebugFn

	// HTTP-specific fields
	Addr                           string
	UseHTTP2                       bool
	RequestWasNotMatchedStatusCode int
	RequestBodyParsers             []RequestBodyParser
	MaxBodyParsingLimit            int64
	NoBodyParsing                  bool
	Middlewares                    []func(http.Handler) http.Handler
	CORS                           *cors.Config
	ServerFactory                  vlatte.ServerFactory[ServerFactoryConfig]
	HandlerDecorator               func(handler http.Handler) http.Handler
	MockFileSearchPatterns         []string
	Loaders                        []Loader
	Proxy                          *ProxyConfig
	Record                         *RecordConfig
	MockFileHandlers               []MockFileHandler
	HTTPClientFactory              func() *http.Client
	HeaderNamesToRedact            map[string]struct{}
	UseHTTPS                       bool
}

var _ vlatte.C = (*Config)(nil)

func (c *Config) SetName(v string)                        { c.Name = v }
func (c *Config) SetAdminPath(v string)                   { c.AdminPath = v }
func (c *Config) SetAdminEnabled(v bool)                  { c.AdminEnabled = v }
func (c *Config) SetRootDir(v string)                     { c.RootDir = v }
func (c *Config) SetTLSConfig(v *tls.Config)              { c.TLSConfig = v }
func (c *Config) SetTLSCertificates(v []tls.Certificate)  { c.TLSCertificates = v }
func (c *Config) SetTLSClientCAs(v *x509.CertPool)        { c.TLSClientCAs = v }
func (c *Config) SetParameters(v kv.KV)                   { c.Parameters = v }
func (c *Config) SetTemplateFunctions(v template.FuncMap) { c.TemplateFunctions = v }
func (c *Config) SetLogger(v *zerolog.Logger)             { c.Logger = v }
func (c *Config) SetLogVerbosity(v vlatte.LogVerbosity)   { c.LogVerbosity = v }
func (c *Config) SetLogLevel(v vlatte.LogLevel)           { c.LogLevel = v }
func (c *Config) SetLogPretty(v bool)                     { c.LogPretty = v }
func (c *Config) SetLogBodyMaxSize(v int64)               { c.LogBodyMaxSize = v }
func (c *Config) SetDebug(v bool)                         { c.Debug = v }
func (c *Config) SetColors(v bool)                        { c.Colors = v }
func (c *Config) SetEventListeners(v *vlatte.Listener)    { c.Listeners = v }
func (c *Config) SetGlobalEvents(v *vlatte.Events)        { c.GlobalEvents = v }
func (c *Config) SetTemplate(v vlatte.Template)           { c.Template = v }
func (c *Config) SetTemplateDataProviders(v []vlatte.TemplateDataProvider) {
	c.TemplateDataProviders = v
}
func (c *Config) SetDebugFn(v vlatte.DebugFn) { c.DebugFn = v }

// Apply copies the current Config struct values to the given Config parameter.
func (c *Config) Apply(conf *Config) error {
	conf.Name = c.Name
	conf.AdminPath = c.AdminPath
	conf.AdminEnabled = c.AdminEnabled
	conf.RootDir = c.RootDir
	conf.TLSConfig = c.TLSConfig
	conf.TLSCertificates = c.TLSCertificates
	conf.TLSClientCAs = c.TLSClientCAs
	conf.Parameters = c.Parameters
	conf.TemplateFunctions = c.TemplateFunctions
	conf.Logger = c.Logger
	conf.LogVerbosity = c.LogVerbosity
	conf.LogLevel = c.LogLevel
	conf.LogPretty = c.LogPretty
	conf.LogBodyMaxSize = c.LogBodyMaxSize
	conf.Debug = c.Debug
	conf.Colors = c.Colors
	conf.Listeners = c.Listeners
	conf.Template = c.Template
	conf.TemplateDataProviders = c.TemplateDataProviders
	conf.DebugFn = c.DebugFn
	conf.Addr = c.Addr
	conf.UseHTTP2 = c.UseHTTP2
	conf.RequestWasNotMatchedStatusCode = c.RequestWasNotMatchedStatusCode
	conf.RequestBodyParsers = c.RequestBodyParsers
	conf.MaxBodyParsingLimit = c.MaxBodyParsingLimit
	conf.NoBodyParsing = c.NoBodyParsing
	conf.Middlewares = c.Middlewares
	conf.CORS = c.CORS
	conf.ServerFactory = c.ServerFactory
	conf.HandlerDecorator = c.HandlerDecorator
	conf.MockFileSearchPatterns = c.MockFileSearchPatterns
	conf.Loaders = c.Loaders
	conf.Proxy = c.Proxy
	conf.Record = c.Record
	conf.UseHTTPS = c.UseHTTPS
	conf.MockFileHandlers = c.MockFileHandlers
	conf.HTTPClientFactory = c.HTTPClientFactory
	conf.HeaderNamesToRedact = c.HeaderNamesToRedact

	return nil
}

// ConfigBuilder lets users create a Config using a fluent API.
type ConfigBuilder struct {
	base                   *vlatte.CB
	conf                   *Config
	recorderConf           []RecordConfigurer
	proxyConf              []ProxyConfigurer
	tlsCertificateFs       string
	tlsKeyFs               string
	tlsClientCertificateFs string
}

func defaultConfig() *Config {
	c := &Config{
		RequestWasNotMatchedStatusCode: StatusNoMatch,
		MockFileSearchPatterns:         ConfigMockFilePattern,
		RequestBodyParsers:             make([]RequestBodyParser, 0),
		Middlewares:                    make([]func(http.Handler) http.Handler, 0),
		Loaders:                        make([]Loader, 0),
		MockFileHandlers:               make([]MockFileHandler, 0),
		HeaderNamesToRedact:            make(map[string]struct{}),
		UseHTTP2:                       true,
		Listeners:                      vlatte.NewListener(),
		GlobalEvents:                   &vlatte.Events{},
	}
	vlatte.ApplyDefaults(c)
	return c
}

// Setup initialize a new ConfigBuilder.
// Entrypoint to start a new custom configuration for HTTPMockApp mock servers.
func Setup() *ConfigBuilder {
	conf := defaultConfig()
	return &ConfigBuilder{
		base: vlatte.NewCB(conf),
		conf: conf,
	}
}

// Base returns the [vlatte.CB] for common configuration.
func (cb *ConfigBuilder) Base() *vlatte.CB {
	return cb.base
}

// Name sets a name to the mock server.
func (cb *ConfigBuilder) Name(name string) *ConfigBuilder {
	cb.base.Name(name)
	return cb
}

// Addr sets a custom address for the mock HTTP server.
func (cb *ConfigBuilder) Addr(addr string) *ConfigBuilder {
	cb.conf.Addr = addr
	return cb
}

// Port sets a custom port to the mock HTTP server.
// If none is provided, a random port number will be used.
func (cb *ConfigBuilder) Port(port int) *ConfigBuilder {
	cb.conf.Addr = ":" + strconv.FormatInt(int64(port), 10)
	return cb
}

// RootDir defines the root directory.
func (cb *ConfigBuilder) RootDir(rootDir string) *ConfigBuilder {
	cb.base.RootDir(rootDir)
	return cb
}

// MockNotFoundStatusCode defines the status code to be used when no mock matches with an HTTP request.
func (cb *ConfigBuilder) MockNotFoundStatusCode(code int) *ConfigBuilder {
	cb.conf.RequestWasNotMatchedStatusCode = code
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

// MaxBodyParsingLimit sets the limit of bytes that request body parsers should read.
// Zero means no limit.
func (cb *ConfigBuilder) MaxBodyParsingLimit(n int64) *ConfigBuilder {
	cb.conf.MaxBodyParsingLimit = n
	return cb
}

// Middlewares adds custom middlewares to the mock server.
// Use this to add custom request interceptors.
func (cb *ConfigBuilder) Middlewares(fn ...func(handler http.Handler) http.Handler) *ConfigBuilder {
	cb.conf.Middlewares = append(cb.conf.Middlewares, fn...)
	return cb
}

// CORS configures CORS for the mock server.
func (cb *ConfigBuilder) CORS(options ...cors.Configurer) *ConfigBuilder {
	opts := cors.DefaultConfig
	for _, option := range options {
		option.Apply(&opts)
	}

	cb.conf.CORS = &opts

	return cb
}

// ServerFactory configures a custom ServerFactory used to build the underlying
// HTTP mock Server for an HTTPMockApp instance.
func (cb *ConfigBuilder) ServerFactory(factory vlatte.ServerFactory[ServerFactoryConfig]) *ConfigBuilder {
	cb.conf.ServerFactory = factory
	return cb
}

// HandlerDecorator configures a custom HTTP handler using the default mock handler.
func (cb *ConfigBuilder) HandlerDecorator(fn func(handler http.Handler) http.Handler) *ConfigBuilder {
	cb.conf.HandlerDecorator = fn
	return cb
}

// Logger sets a custom zerolog.Logger.
func (cb *ConfigBuilder) Logger(l *zerolog.Logger) *ConfigBuilder {
	cb.base.Logger(l)
	return cb
}

func (cb *ConfigBuilder) Silent() *ConfigBuilder {
	cb.base.Silent()
	return cb
}

// LogVerbosity sets the verbosity of informative logs.
func (cb *ConfigBuilder) LogVerbosity(l vlatte.LogVerbosity) *ConfigBuilder {
	cb.base.LogVerbosity(l)
	return cb
}

// LogLevel sets the level of the zerolog.Logger.
func (cb *ConfigBuilder) LogLevel(l vlatte.LogLevel) *ConfigBuilder {
	cb.base.LogLevel(l)
	return cb
}

// LogPretty enable/disable pretty logging.
func (cb *ConfigBuilder) LogPretty(v bool) *ConfigBuilder {
	cb.base.LogPretty(v)
	return cb
}

// LogBodyMaxSize sets the max size of the response body to be logged.
func (cb *ConfigBuilder) LogBodyMaxSize(max int64) *ConfigBuilder {
	cb.base.LogBodyMaxSize(max)
	return cb
}

// UseDescriptiveLogger enable/disable the use of a more descriptive logger.
func (cb *ConfigBuilder) UseDescriptiveLogger() *ConfigBuilder {
	cb.base.Debug(true)
	return cb
}

// Colors enable/disable use of ANSI colors in logs.
func (cb *ConfigBuilder) Colors(v bool) *ConfigBuilder {
	cb.base.Colors(v)
	return cb
}

// RedactHeader redacts the given HTTP header names in logs.
func (cb *ConfigBuilder) RedactHeader(names ...string) *ConfigBuilder {
	for _, name := range names {
		cb.conf.HeaderNamesToRedact[strings.ToLower(name)] = struct{}{}
	}

	return cb
}

// Parameters sets a custom reply parameters store.
func (cb *ConfigBuilder) Parameters(params kv.KV) *ConfigBuilder {
	cb.base.Parameters(params)
	return cb
}

// MockFilePatterns sets custom Glob patterns to load mock from the file system.
// Defaults to [testdata/*.mock.json, testdata/*.mock.yaml].
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

// TemplateEngineFunctions sets custom functions to be used in templates.
func (cb *ConfigBuilder) TemplateEngineFunctions(fm template.FuncMap) *ConfigBuilder {
	cb.base.TemplateFunctions(fm)
	return cb
}

// HTTPClient sets a custom http.Client factory.
// Internal components that require an HTTP client will use this factory,
// instead of using the default implementation.
func (cb *ConfigBuilder) HTTPClient(f func() *http.Client) *ConfigBuilder {
	cb.conf.HTTPClientFactory = f
	return cb
}

// TLSConfig defines custom TLS configurations.
func (cb *ConfigBuilder) TLSConfig(c *tls.Config) *ConfigBuilder {
	cb.base.TLSConfig(c)
	return cb
}

// TLSCertKeyPair sets a custom public/private key pair.
// If none is provided, default values will be used when starting the server with HTTPMockApp.StartTLS or HTTPMockApp.MustStartTLS.
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

// OnCompleteListener registers an on-complete event listener by name.
func (cb *ConfigBuilder) OnCompleteListener(name string, listener vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
	cb.conf.Listeners.OnComplete.Register(name, listener)
	return cb
}

// DebugFn sets the debug function.
func (cb *ConfigBuilder) DebugFn(dfn vlatte.DebugFn) *ConfigBuilder {
	cb.base.DebugFn(dfn)
	return cb
}

// AdminPath sets the Admin path.
func (cb *ConfigBuilder) AdminPath(path string) *ConfigBuilder {
	cb.base.AdminPath(path)
	return cb
}

// AdminEnabled enable/disable Admin endpoints.
func (cb *ConfigBuilder) AdminEnabled(v bool) *ConfigBuilder {
	cb.base.AdminEnabled(v)
	return cb
}

// Template sets the template template.
func (cb *ConfigBuilder) Template(t vlatte.Template) *ConfigBuilder {
	cb.base.Template(t)
	return cb
}

// TemplateDataProviders sets template data providers.
func (cb *ConfigBuilder) TemplateDataProviders(sdp []vlatte.TemplateDataProvider) *ConfigBuilder {
	cb.base.TemplateDataProviders(sdp)
	return cb
}

// Apply builds a new Config with previously configured values.
func (cb *ConfigBuilder) Apply(conf *Config) error {
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

	if len(cb.tlsCertificateFs) == 0 && len(cb.tlsKeyFs) == 0 {
		return cb.conf.Apply(conf)
	}

	if err := cb.base.TLSFromFiles(cb.tlsCertificateFs, cb.tlsKeyFs, cb.tlsClientCertificateFs); err != nil {
		return fmt.Errorf("config: failed to build TLS config: %w", err)
	}

	return cb.conf.Apply(conf)
}
