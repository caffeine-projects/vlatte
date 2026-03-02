package vlatte

import (
	"crypto/tls"
	"crypto/x509"
	"text/template"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/shelf/kv"
)

// Config holds base mock server configurations.
type Config struct {
	Name                string
	AdminPath           string
	AdminEnabled        bool
	RootDir             string
	TLSConfig           *tls.Config
	TLSCertificates     []tls.Certificate
	TLSClientCAs        *x509.CertPool
	Parameters          kv.KV
	TemplateFunctions   template.FuncMap
	Logger              *zerolog.Logger
	LogVerbosity        LogVerbosity
	LogLevel            LogLevel
	LogPretty           bool
	LogBodyMaxSize      int64
	Debug               bool
	Colors              bool
	Events              *Events
	Sketch              Template
	SketchDataProviders []TemplateDataProvider
	DebugFn             DebugFn
}

// Apply copies the current Config values to the given Config.
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
	conf.Events = c.Events
	conf.Sketch = c.Sketch
	conf.SketchDataProviders = c.SketchDataProviders
	conf.DebugFn = c.DebugFn

	return nil
}

// Configurer lets users configure the Mock API.
type Configurer[TConfig any] interface {
	Apply(conf TConfig) error
}

// DebugFn is a function for debug logging.
type DebugFn func(string, any)

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		AdminPath:   "/__admin",
		Events:      NewEvents(),
		LogPretty:    true,
		LogLevel:     LogLevelInfo,
		LogVerbosity: LogHeader,
		Colors:       true,
		DebugFn:      func(evt string, a any) {},
	}
}
