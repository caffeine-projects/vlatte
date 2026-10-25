package mockgrpc

import (
	"net"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	"github.com/caffeine-projects/vlatte/journal"
)

type Config struct {
	Service       any
	ServiceDesc   *grpc.ServiceDesc
	ServerOptions []grpc.ServerOption
	Addr          string
	Listener      net.Listener // in place of listening on Addr: see ConfigBuilder.Listener
	Logger        *zerolog.Logger

	// NoDebugHeaders turns off the debug header metadata, such as vlatte-matched-mock-id, of the server's answers.
	NoDebugHeaders bool

	// Admin serves the admin API, on an HTTP server of its own. Nil serves none.
	Admin *AdminConfig

	// Journal keeps a journal of the calls the server serves. Nil keeps none.
	Journal *JournalConfig

	// NoNearMisses turns off the report of the mocks closest to a call no mock matched, which its journal entry
	// lists. The report runs no matcher twice: it runs those matching never reached.
	NoNearMisses bool
}

// AdminConfig configures the admin API of a server: a RESTful HTTP server of its own, the same as HTTP mock servers
// have. The operations gRPC mocks can't do, such as adding mocks from documents, answer 501.
type AdminConfig struct {
	// Addr is the address the admin API listens on: host:port, :port or a port. Empty is a random local port.
	Addr string
}

// JournalConfig configures the journal of a server: the calls its mocks get, of every outcome, which tests and the
// admin API verify and search, while the server runs: Close closes it. The calls an interceptor of ServerOptions
// answers aren't in it. An entry has the code the client gets, after every interceptor of ServerOptions: a stream
// interceptor goes through [grpc.ChainStreamInterceptor] then, see [ErrStreamInterceptorSlot].
type JournalConfig struct {
	// Dir is where the journal keeps its files, across restarts. Empty keeps them in a temporary directory, which
	// Close removes.
	Dir string

	// MaxEntries is how many calls the journal keeps, the newest. 0 keeps them all.
	MaxEntries int

	// MaxBodySize is how many bytes of a call's request message, as JSON, the journal keeps. 0 keeps whole messages.
	MaxBodySize int64
}

func (c *Config) Apply(conf *Config) error {
	conf.Service = c.Service
	conf.ServiceDesc = c.ServiceDesc
	conf.ServerOptions = c.ServerOptions
	conf.Addr = c.Addr
	conf.Listener = c.Listener
	conf.Logger = c.Logger
	conf.NoDebugHeaders = c.NoDebugHeaders
	conf.Admin = c.Admin
	conf.Journal = c.Journal
	conf.NoNearMisses = c.NoNearMisses

	return nil
}

func defaultConfig() *Config {
	return &Config{Addr: "127.0.0.1:0"}
}

type ConfigBuilder struct {
	conf *Config
}

func Setup() *ConfigBuilder {
	return &ConfigBuilder{conf: defaultConfig()}
}

func (cb *ConfigBuilder) Service(sd *grpc.ServiceDesc, service any) *ConfigBuilder {
	cb.conf.ServiceDesc = sd
	cb.conf.Service = service

	return cb
}

// ServerOptions sets the options of the gRPC server, such as interceptors, which run before the mocks. With a
// journal, a stream interceptor goes through [grpc.ChainStreamInterceptor], not [grpc.StreamInterceptor], which the
// journal takes: [New] fails with [ErrStreamInterceptorSlot].
func (cb *ConfigBuilder) ServerOptions(options ...grpc.ServerOption) *ConfigBuilder {
	cb.conf.ServerOptions = options
	return cb
}

// Logger sets the logger used by the mock GRPC server. Defaults to a logger writing to the standard output.
func (cb *ConfigBuilder) Logger(logger *zerolog.Logger) *ConfigBuilder {
	cb.conf.Logger = logger
	return cb
}

// Addr sets a custom address for the mock GRPC server.
func (cb *ConfigBuilder) Addr(addr string) *ConfigBuilder {
	cb.conf.Addr = addr
	return cb
}

// Listener sets an already bound listener for the mock GRPC server to serve instead of listening on Addr.
// Start takes it: Close, or a Start that fails, closes it, so nothing listens without a server behind it.
func (cb *ConfigBuilder) Listener(l net.Listener) *ConfigBuilder {
	cb.conf.Listener = l
	return cb
}

// NoDebugHeaders turns off the debug header metadata, such as vlatte-matched-mock-id, that the server adds to its
// answers. The vlatte-error trailer stays: it tells the server's own answers from a mock's.
func (cb *ConfigBuilder) NoDebugHeaders() *ConfigBuilder {
	cb.conf.NoDebugHeaders = true
	return cb
}

func (cb *ConfigBuilder) Apply(conf *Config) error {
	return cb.conf.Apply(conf)
}

// Admin serves the admin API, on an HTTP server of its own at addr: host:port, :port or a port. Empty is a random
// local port. Server.AdminURL returns where it listens.
func (cb *ConfigBuilder) Admin(addr string) *ConfigBuilder {
	cb.conf.Admin = &AdminConfig{Addr: addr}
	return cb
}

// Journal keeps a journal of the calls the server serves, in dir, across restarts: see JournalConfig. Empty keeps it
// in a temporary directory, which Close removes, as a test wants: a named one keeps the calls of earlier runs.
func (cb *ConfigBuilder) Journal(dir string) *ConfigBuilder {
	cb.conf.Journal = &JournalConfig{
		Dir:         dir,
		MaxEntries:  journal.DefaultMaxEntries,
		MaxBodySize: journal.DefaultMaxBodySize,
	}

	return cb
}

// NoNearMisses turns off the report of the mocks closest to a call no mock matched: see Config.NoNearMisses.
func (cb *ConfigBuilder) NoNearMisses() *ConfigBuilder {
	cb.conf.NoNearMisses = true
	return cb
}
