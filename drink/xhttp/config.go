package xhttp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp/cors"
	"github.com/caffeine-projects/vlatte/shelf/kv"
)

// Defaults
var (
	// ConfigMockFilePattern is the default filename glob pattern to search for local mock files.
	ConfigMockFilePattern = []string{"testdata/_mocks/*mock.yaml", "testdata/_mocks/*mock.yml"}
)

// Config holds HTTPMockApp mock server configurations.
type Config struct {
	vlatte.Config

	Addr                           string
	UseHTTP2                       bool
	RequestWasNotMatchedStatusCode int
	RequestBodyParsers             []RequestBodyParser
	MaxBodyParsingLimit            int64
	NoBodyParsing                  bool
	Middlewares                    []func(http.Handler) http.Handler
	CORS                           *cors.Config
	Server                         Server
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

// Apply copies the current Config struct values to the given Config parameter.
func (c *Config) Apply(conf *Config) error {
	if err := c.Config.Apply(&conf.Config); err != nil {
		return err
	}
	conf.Addr = c.Addr
	conf.UseHTTP2 = c.UseHTTP2
	conf.RequestWasNotMatchedStatusCode = c.RequestWasNotMatchedStatusCode
	conf.RequestBodyParsers = c.RequestBodyParsers
	conf.MaxBodyParsingLimit = c.MaxBodyParsingLimit
	conf.NoBodyParsing = c.NoBodyParsing
	conf.Middlewares = c.Middlewares
	conf.CORS = c.CORS
	conf.Server = c.Server
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
	conf                   *Config
	recorderConf           []RecordConfigurer
	proxyConf              []ProxyConfigurer
	tlsCertificateFs       string
	tlsKeyFs               string
	tlsClientCertificateFs string
}

func defaultConfig() *Config {
	base := vlatte.DefaultConfig()
	return &Config{
		Config:                         *base,
		RequestWasNotMatchedStatusCode: StatusNoMatch,
		MockFileSearchPatterns:         ConfigMockFilePattern,
		RequestBodyParsers:             make([]RequestBodyParser, 0),
		Middlewares:                    make([]func(http.Handler) http.Handler, 0),
		Loaders:                        make([]Loader, 0),
		MockFileHandlers:               make([]MockFileHandler, 0),
		HeaderNamesToRedact:            make(map[string]struct{}),
		UseHTTP2:                       true,
	}
}

// Setup initialize a new ConfigBuilder.
// Entrypoint to start a new custom configuration for HTTPMockApp mock servers.
func Setup() *ConfigBuilder {
	conf := defaultConfig()
	return &ConfigBuilder{
		conf: conf,
	}
}

// Name sets a name to the mock server.
func (cb *ConfigBuilder) Name(name string) *ConfigBuilder {
	cb.conf.Name = name
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
	cb.conf.RootDir = rootDir
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

// Server configures a custom HTTP mock Server.
func (cb *ConfigBuilder) Server(srv Server) *ConfigBuilder {
	cb.conf.Server = srv
	return cb
}

// HandlerDecorator configures a custom HTTP handler using the default mock handler.
func (cb *ConfigBuilder) HandlerDecorator(fn func(handler http.Handler) http.Handler) *ConfigBuilder {
	cb.conf.HandlerDecorator = fn
	return cb
}

// Logger sets a custom zerolog.Logger.
func (cb *ConfigBuilder) Logger(l *zerolog.Logger) *ConfigBuilder {
	cb.conf.Logger = l
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
func (cb *ConfigBuilder) LogBodyMaxSize(max int64) *ConfigBuilder {
	cb.conf.LogBodyMaxSize = max
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

// RedactHeader redacts the given HTTP header names in logs.
func (cb *ConfigBuilder) RedactHeader(names ...string) *ConfigBuilder {
	for _, name := range names {
		cb.conf.HeaderNamesToRedact[strings.ToLower(name)] = struct{}{}
	}

	return cb
}

// Parameters sets a custom reply parameters store.
func (cb *ConfigBuilder) Parameters(params kv.KV) *ConfigBuilder {
	cb.conf.Parameters = params
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
	cb.conf.TemplateFunctions = fm
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
	cb.conf.TLSConfig = c
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

// PostAction registers a PostAction by name.
func (cb *ConfigBuilder) PostAction(name string, action vlatte.PostAction) *ConfigBuilder {
	if cb.conf.PostActions == nil {
		cb.conf.PostActions = make(map[string]vlatte.PostAction)
	}
	cb.conf.PostActions[name] = action
	return cb
}

// DebugFn sets the debug function.
func (cb *ConfigBuilder) DebugFn(dfn vlatte.DebugFn) *ConfigBuilder {
	cb.conf.DebugFn = dfn
	return cb
}

// AdminPath sets the Admin path.
func (cb *ConfigBuilder) AdminPath(path string) *ConfigBuilder {
	cb.conf.AdminPath = path
	return cb
}

// AdminEnabled enable/disable Admin endpoints.
func (cb *ConfigBuilder) AdminEnabled(v bool) *ConfigBuilder {
	cb.conf.AdminEnabled = v
	return cb
}

// Sketch sets the sketch template.
func (cb *ConfigBuilder) Sketch(sketch vlatte.Template) *ConfigBuilder {
	cb.conf.Sketch = sketch
	return cb
}

// SketchDataProviders sets template data providers.
func (cb *ConfigBuilder) SketchDataProviders(sdp []vlatte.TemplateDataProvider) *ConfigBuilder {
	cb.conf.SketchDataProviders = sdp
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

	err := applyTLS(cb.conf, cb.tlsCertificateFs, cb.tlsKeyFs, cb.tlsClientCertificateFs)
	if err != nil {
		return err
	}

	return cb.conf.Apply(conf)
}

func applyTLS(c *Config, certFile, keyFile, clientCertFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("config: failed to load certificate key pair from file=%s key=%s. %w", certFile, keyFile, err)
	}

	if len(clientCertFile) > 0 {
		cc, err := os.ReadFile(filepath.Clean(clientCertFile))
		if err != nil {
			return fmt.Errorf("config: failed to load client certificate from %s. %w", clientCertFile, err)
		}

		cp := x509.NewCertPool()
		cp.AppendCertsFromPEM(cc)

		c.TLSClientCAs = cp
	}

	c.TLSCertificates = []tls.Certificate{cert}

	return nil
}
