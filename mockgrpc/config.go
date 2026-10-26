package mockgrpc

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

// _defaultShutdownTimeout is how long Close waits for the calls in flight, unless Config.ShutdownTimeout says.
const _defaultShutdownTimeout = 5 * time.Second

// Config configures a Server. As an Option, it sets every field of the server's configuration, its zero values
// included: [Setup] sets only what its methods set, so its options compose.
type Config struct {
	// Name names the server in its log lines.
	Name string

	// ConfigSources are the sources of settings New reads after the options, which they override: see
	// DefaultConfigSources, which nil reads. An empty list reads none.
	ConfigSources []ConfigSource

	// Serve reports whether the settings turned the grpc section on: the vlatte command serves gRPC mocks then.
	Serve bool

	Service       any
	ServiceDesc   *grpc.ServiceDesc
	ServerOptions []grpc.ServerOption
	Addr          string
	Listener      net.Listener // in place of listening on Addr: see ConfigBuilder.Listener

	// InMemory serves the server in memory, in place of listening on Addr: see ConfigBuilder.InMemory.
	InMemory bool

	// Reflection serves the gRPC server reflection service: see ConfigBuilder.Reflection.
	Reflection bool

	// Logger is the server's logger. Nil builds one that writes to the standard output, at LogLevel.
	Logger *zerolog.Logger

	// LogLevel is the level of the logger the server builds.
	LogLevel vlatte.LogLevel

	// LogOutput is where the logger the server builds writes. Nil is the standard output.
	LogOutput io.Writer

	// NoDebugHeaders turns off the debug header metadata, such as vlatte-matched-mock-id, of the server's answers.
	NoDebugHeaders bool

	// NoMatchCode is the code of the answer to a call no mock matched, with the vlatte-error trailer. OK means
	// codes.NotFound.
	NoMatchCode codes.Code

	// Files holds the descriptors of services the program has no generated code of, which mocks can serve as if it
	// had: see ConfigBuilder.Files.
	Files *protoregistry.Files

	// Descriptors are paths of descriptor set files, which the server loads as it does Files: see
	// ConfigBuilder.Descriptors.
	Descriptors []string

	// Listeners holds the on_complete listeners mocks name, such as webhooks. Nil gives the server a registry of its
	// own.
	Listeners *vlatte.Listener

	// GlobalEvents are the events that run for every mock.
	GlobalEvents *vlatte.Events

	// TemplateEngine compiles the templates of replies and events. Nil uses a vlatte.GoTemplateEngine.
	TemplateEngine vlatte.TemplateEngine

	// Parameters are the parameters templates read. Nil keeps them in memory.
	Parameters vlatte.KV

	// RootDir is where relative MockFiles patterns start. Empty is the working directory.
	RootDir string

	// MockFiles are glob patterns of the mock files the server loads at Start and on Reload.
	MockFiles []string

	// ShutdownTimeout is how long Close waits for the calls in flight before it cuts them. 0 means 5s.
	ShutdownTimeout time.Duration

	// Admin serves the admin API, on an HTTP server of its own. Nil serves none.
	Admin AdminAPI

	// Journal keeps a journal of the calls the server serves. Nil keeps none.
	Journal *JournalConfig

	// Record records the calls the server proxies as mock files, from Start to Close, and serves the recordings of its
	// directory: see RecordConfig. Nil records nothing, until Server.StartRecording.
	Record *RecordConfig

	// NoNearMisses turns off the report of the mocks closest to a call no mock matched, which its journal entry
	// lists. The report runs no matcher twice: it runs those matching never reached.
	NoNearMisses bool
}

// AdminAPI is an admin API the server opts into: a RESTful HTTP server of its own, the same as HTTP mock servers have.
// Make one with [github.com/caffeine-projects/vlatte/admin.New]. The operations gRPC mocks can't do, such as recording,
// answer 501.
type AdminAPI = adminapi.API

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

// Apply implements Option: it sets every field of conf to c's.
func (c *Config) Apply(conf *Config) error {
	*conf = *c
	return nil
}

func defaultConfig() *Config {
	return &Config{
		Addr:     "127.0.0.1:0",
		LogLevel: vlatte.LogLevelInfo,
	}
}

// ConfigBuilder builds the configuration of a Server. Each method sets one setting, and Apply sets only those: the
// options passed to New compose, so a later one changes only what it sets.
type ConfigBuilder struct {
	sets []func(conf *Config) error
}

// Setup returns an empty ConfigBuilder.
func Setup() *ConfigBuilder {
	return &ConfigBuilder{}
}

// Name names the server in its log lines.
func (cb *ConfigBuilder) Name(name string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Name = name })
}

// Service registers impl as the implementation of the service sd describes, as RegisterService does. The mocks answer
// its calls: a service needs no registration to be mocked, so this is for the services whose generated code the
// program doesn't otherwise have registered.
func (cb *ConfigBuilder) Service(sd *grpc.ServiceDesc, impl any) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.ServiceDesc, conf.Service = sd, impl })
}

// ServerOptions adds options of the gRPC server, such as interceptors, which run before the mocks. With a journal, a
// stream interceptor goes through [grpc.ChainStreamInterceptor], not [grpc.StreamInterceptor], which the journal
// takes: [New] fails with [ErrStreamInterceptorSlot].
func (cb *ConfigBuilder) ServerOptions(options ...grpc.ServerOption) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.ServerOptions = append(conf.ServerOptions, options...) })
}

// Logger sets the logger of the server. Without one, the server logs to the standard output, at LogLevel.
func (cb *ConfigBuilder) Logger(logger *zerolog.Logger) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Logger = logger })
}

// LogLevel sets the level of the logger the server builds.
func (cb *ConfigBuilder) LogLevel(l vlatte.LogLevel) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogLevel = l })
}

// LogOutput sets where the logger the server builds writes, in place of the standard output, such as for a program
// that keeps the standard output for something else. A Logger writes where it was told to.
func (cb *ConfigBuilder) LogOutput(w io.Writer) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.LogOutput = w })
}

// Silent turns off the logs of the logger the server builds.
func (cb *ConfigBuilder) Silent() *ConfigBuilder {
	return cb.LogLevel(vlatte.LogLevelDisabled)
}

// Addr sets the address the server listens on. The default is a random local port: see Server.Addr.
func (cb *ConfigBuilder) Addr(addr string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Addr = addr })
}

// Listener sets an already bound listener for the mock GRPC server to serve instead of listening on Addr.
// Start takes it: Close, or a Start that fails, closes it, so nothing listens without a server behind it.
func (cb *ConfigBuilder) Listener(l net.Listener) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Listener = l })
}

// InMemory serves the server in memory, in place of listening on a port: only the connections of Server.Dial reach
// it. Tests that run in parallel need no free port, and nothing outside the test can call the server. New fails for a
// server given a Listener too.
func (cb *ConfigBuilder) InMemory() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.InMemory = true })
}

// Reflection serves the gRPC server reflection service, so tools such as grpcurl and Postman list the services the
// server serves, and describe their messages: those registered on it, those of its descriptors, and those its mocks
// name.
func (cb *ConfigBuilder) Reflection() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Reflection = true })
}

// NoDebugHeaders turns off the debug header metadata, such as vlatte-matched-mock-id, that the server adds to its
// answers. The vlatte-error trailer stays: it tells the server's own answers from a mock's.
func (cb *ConfigBuilder) NoDebugHeaders() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoDebugHeaders = true })
}

// NoMatchCode sets the code of the answer to a call no mock matched. The default is codes.NotFound. The answer has
// the vlatte-error trailer either way.
func (cb *ConfigBuilder) NoMatchCode(code codes.Code) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoMatchCode = code })
}

// Files gives the server the descriptors of services the program has no generated code of: mocks name their methods,
// and the server serves their calls with messages of dynamic types. Generated code registers its own descriptors, in
// protoregistry.GlobalFiles, which the server always reads.
func (cb *ConfigBuilder) Files(files *protoregistry.Files) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Files = files })
}

// Descriptors gives the server the descriptor set files at paths, as Files does: such as protoc writes with
// --descriptor_set_out and --include_imports, or buf build -o. New fails on a file it can't read.
func (cb *ConfigBuilder) Descriptors(paths ...string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Descriptors = append(conf.Descriptors, paths...) })
}

// OnCompleteListener registers listener under name, for the on_complete events of mocks that name it: see
// [vlatte.EventConfig]. It registers when the server is built, in the server's registry, or in the one
// EventListeners set: a name it has already fails New with vlatte.ErrEventAlreadyRegistered.
func (cb *ConfigBuilder) OnCompleteListener(name string, listener vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		if conf.Listeners == nil {
			conf.Listeners = vlatte.NewListener()
		}

		if err := conf.Listeners.OnComplete.Register(name, listener); err != nil {
			return fmt.Errorf("config: on_complete listener %s: %w", name, err)
		}

		return nil
	})

	return cb
}

// EventListeners sets the registry of named event listeners, such as one several servers share.
func (cb *ConfigBuilder) EventListeners(listeners *vlatte.Listener) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Listeners = listeners })
}

// GlobalEvents sets the events that run for every mock, before the mock's own.
func (cb *ConfigBuilder) GlobalEvents(events *vlatte.Events) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.GlobalEvents = events })
}

// TemplateEngine sets the engine that compiles the templates of replies and events. The default is a
// vlatte.GoTemplateEngine.
func (cb *ConfigBuilder) TemplateEngine(e vlatte.TemplateEngine) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.TemplateEngine = e })
}

// Parameters sets the parameters templates read: {{ .Parameters.Get "key" }}.
func (cb *ConfigBuilder) Parameters(params vlatte.KV) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Parameters = params })
}

// RootDir sets where relative MockFiles patterns start. The default is the working directory.
func (cb *ConfigBuilder) RootDir(dir string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.RootDir = dir })
}

// MockFiles adds glob patterns of mock files, such as "testdata/grpc/*.yaml", which the server loads at Start, before
// it serves, and again on Reload. See FromFile for their format.
func (cb *ConfigBuilder) MockFiles(patterns ...string) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.MockFiles = append(conf.MockFiles, patterns...) })
}

// ShutdownTimeout sets how long Close waits for the calls in flight, such as a stream a client never ends, before it
// cuts them. The default is 5s.
func (cb *ConfigBuilder) ShutdownTimeout(d time.Duration) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.ShutdownTimeout = d })
}

// ConfigSources sets the sources of settings New reads after the options, which they override: see
// DefaultConfigSources. Without it, New reads DefaultConfigSources(nil): the grpc section of .vlatterc, and the
// VLATTE_GRPC_* environment variables. Call it with none to read none.
func (cb *ConfigBuilder) ConfigSources(sources ...ConfigSource) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.ConfigSources = append(make([]ConfigSource, 0, len(sources)), sources...) })
}

// Admin serves the admin API api, such as admin.MustNew(admin.Addr("127.0.0.1:8081")), on an HTTP server of its own.
// Server.AdminURL returns where it listens.
func (cb *ConfigBuilder) Admin(api AdminAPI) *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.Admin = api })
}

// Journal keeps a journal of the calls the server serves, in dir, across restarts: see JournalConfig. Empty keeps it
// in a temporary directory, which Close removes, as a test wants: a named one keeps the calls of earlier runs.
func (cb *ConfigBuilder) Journal(dir string) *ConfigBuilder {
	return cb.set(func(conf *Config) {
		conf.Journal = &JournalConfig{
			Dir:         dir,
			MaxEntries:  journal.DefaultMaxEntries,
			MaxBodySize: journal.DefaultMaxBodySize,
		}
	})
}

// NoNearMisses turns off the report of the mocks closest to a call no mock matched: see Config.NoNearMisses.
func (cb *ConfigBuilder) NoNearMisses() *ConfigBuilder {
	return cb.set(func(conf *Config) { conf.NoNearMisses = true })
}

// Record records the calls the server proxies, from Start to Close, with the default settings, which options change:
// see RecordConfig.
func (cb *ConfigBuilder) Record(options ...RecordConfigurer) *ConfigBuilder {
	cb.sets = append(cb.sets, func(conf *Config) error {
		rec := defaultRecordConfig()
		for i, option := range options {
			if err := option.Apply(rec); err != nil {
				return fmt.Errorf("config: record option at index %d with type %T: %w", i, option, err)
			}
		}

		conf.Record = rec

		return nil
	})

	return cb
}

// Apply implements Option: it sets, in order, what the methods of cb set, and leaves the rest of conf as it is.
func (cb *ConfigBuilder) Apply(conf *Config) error {
	for _, set := range cb.sets {
		if err := set(conf); err != nil {
			return err
		}
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
