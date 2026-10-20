package mockgrpc

import (
	"net"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
)

type Config struct {
	Service       any
	ServiceDesc   *grpc.ServiceDesc
	ServerOptions []grpc.ServerOption
	Addr          string
	Listener      net.Listener
	Logger        *zerolog.Logger

	// NoDebugHeaders turns off the debug header metadata, such as vlatte-matched-mock-id, of the server's answers.
	NoDebugHeaders bool

	// Admin serves the admin API, on an HTTP server of its own. Nil serves none.
	Admin *AdminConfig
}

// AdminConfig configures the admin API of a server: a RESTful HTTP server of its own, the same as HTTP mock servers
// have. The operations gRPC mocks can't do, such as adding mocks from documents, answer 501.
type AdminConfig struct {
	// Addr is the address the admin API listens on: host:port, :port or a port. Empty is a random local port.
	Addr string
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
// The server closes it on Close.
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
