package vlatte

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"text/template"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/vlattecore/kv"
)

// C is the interface for common mock server configuration.
// Specific configs (e.g. xhttp.Config) implement C and can be built with [CB].
type C interface {
	SetName(string)
	SetAdminPath(string)
	SetRootDir(string)
	SetTLSConfig(*tls.Config)
	SetTLSCertificates([]tls.Certificate)
	SetTLSClientCAs(*x509.CertPool)
	SetParameters(kv.KV)
	SetTemplateFunctions(template.FuncMap)
	SetLogger(*zerolog.Logger)
	SetLogVerbosity(LogVerbosity)
	SetLogLevel(LogLevel)
	SetLogPretty(bool)
	SetLogBodyMaxSize(int64)
	SetDebug(bool)
	SetColors(bool)
	SetEventListeners(*Listener)
	SetGlobalEvents(*Events)
	SetTemplate(Template)
	SetTemplateDataProviders([]TemplateDataProvider)
	SetDebugFn(DebugFn)
}

// Configurer lets users configure the Mock API.
type Configurer[TConfig any] interface {
	Apply(conf TConfig) error
}

// DebugFn is a function for debug logging.
type DebugFn func(string, any)

// Option is a function that sets a configuration on a [C].
type Option[T C] func(T) error

// OptionFunc is a function that sets a configuration on a [C].
type OptionFunc[T C] func(T) error

// Apply implements [Configurer].
func (o OptionFunc[T]) Apply(t T) error {
	return o(t)
}

// CB sets common configuration fields on any [C].
type CB struct {
	conf C
}

// NewCB returns a builder that sets common fields on conf.
func NewCB(conf C) *CB {
	return &CB{conf: conf}
}

// Name sets the mock server name.
func (cb *CB) Name(name string) *CB {
	cb.conf.SetName(name)
	return cb
}

// AdminPath sets the admin API path.
func (cb *CB) AdminPath(path string) *CB {
	cb.conf.SetAdminPath(path)
	return cb
}

// RootDir sets the root directory.
func (cb *CB) RootDir(dir string) *CB {
	cb.conf.SetRootDir(dir)
	return cb
}

// TLSConfig sets the TLS config.
func (cb *CB) TLSConfig(c *tls.Config) *CB {
	cb.conf.SetTLSConfig(c)
	return cb
}

// TLSCertificates sets the TLS certificates.
func (cb *CB) TLSCertificates(certs []tls.Certificate) *CB {
	cb.conf.SetTLSCertificates(certs)
	return cb
}

// TLSClientCAs sets the client CA pool for mutual TLS.
func (cb *CB) TLSClientCAs(pool *x509.CertPool) *CB {
	cb.conf.SetTLSClientCAs(pool)
	return cb
}

// Parameters sets the parameters store.
func (cb *CB) Parameters(params kv.KV) *CB {
	cb.conf.SetParameters(params)
	return cb
}

// TemplateFunctions sets the template func map.
func (cb *CB) TemplateFunctions(fm template.FuncMap) *CB {
	cb.conf.SetTemplateFunctions(fm)
	return cb
}

// Logger sets the logger.
func (cb *CB) Logger(l *zerolog.Logger) *CB {
	cb.conf.SetLogger(l)
	return cb
}

// LogVerbosity sets the log verbosity.
func (cb *CB) LogVerbosity(v LogVerbosity) *CB {
	cb.conf.SetLogVerbosity(v)
	return cb
}

// LogLevel sets the log level.
func (cb *CB) LogLevel(v LogLevel) *CB {
	cb.conf.SetLogLevel(v)
	return cb
}

// LogPretty sets whether logging is pretty-printed.
func (cb *CB) LogPretty(v bool) *CB {
	cb.conf.SetLogPretty(v)
	return cb
}

// LogBodyMaxSize sets the max body size for logging.
func (cb *CB) LogBodyMaxSize(n int64) *CB {
	cb.conf.SetLogBodyMaxSize(n)
	return cb
}

// Debug sets the debug flag.
func (cb *CB) Debug(v bool) *CB {
	cb.conf.SetDebug(v)
	return cb
}

// Colors sets whether to use colors in logs.
func (cb *CB) Colors(v bool) *CB {
	cb.conf.SetColors(v)
	return cb
}

// EventListeners sets the event listeners.
func (cb *CB) EventListeners(e *Listener) *CB {
	cb.conf.SetEventListeners(e)
	return cb
}

// GlobalEvents sets the global events.
func (cb *CB) GlobalEvents(e *Events) *CB {
	cb.conf.SetGlobalEvents(e)
	return cb
}

// Template sets the template template.
func (cb *CB) Template(t Template) *CB {
	cb.conf.SetTemplate(t)
	return cb
}

// TemplateDataProviders sets the template data providers.
func (cb *CB) TemplateDataProviders(p []TemplateDataProvider) *CB {
	cb.conf.SetTemplateDataProviders(p)
	return cb
}

// DebugFn sets the debug function.
func (cb *CB) DebugFn(fn DebugFn) *CB {
	cb.conf.SetDebugFn(fn)
	return cb
}

// Silent disables log output (same as LogLevel(LogLevelDisabled)).
func (cb *CB) Silent() *CB {
	cb.conf.SetLogLevel(LogLevelDisabled)
	return cb
}

// TLSFromFiles loads TLS material from the given files and configures TLS on the underlying [C].
// If clientCertFile is empty, only the server certificate/key are configured.
func (cb *CB) TLSFromFiles(certFile, keyFile, clientCertFile string) error {
	return ApplyTLS(cb.conf, certFile, keyFile, clientCertFile)
}

type CBEvt[RET any] struct {
	ret              RET
	OnCompleteEvents []EventConfig
}

func (cb *CBEvt[RET]) OnComplete(name string, parameters Parameters) RET {
	cb.OnCompleteEvents = append(cb.OnCompleteEvents, EventConfig{
		Name:       name,
		Parameters: parameters,
	})

	return cb.ret
}

// ApplyDefaults sets default values on c for common fields.
func ApplyDefaults(c C) {
	c.SetAdminPath("/__admin")
	c.SetEventListeners(NewListener())
	c.SetLogPretty(true)
	c.SetLogLevel(LogLevelInfo)
	c.SetLogVerbosity(LogHeader)
	c.SetColors(true)
	c.SetDebugFn(func(evt string, a any) {})
}

// ApplyTLS loads TLS material from the given files and configures TLS on c.
// It is a shared helper for all mock server config builders.
func ApplyTLS(c C, certFile, keyFile, clientCertFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	if len(clientCertFile) > 0 {
		cc, err := os.ReadFile(filepath.Clean(clientCertFile))
		if err != nil {
			return err
		}

		cp := x509.NewCertPool()
		cp.AppendCertsFromPEM(cc)

		c.SetTLSClientCAs(cp)
	}

	c.SetTLSCertificates([]tls.Certificate{cert})

	return nil
}
