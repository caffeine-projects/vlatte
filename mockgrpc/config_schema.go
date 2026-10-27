package mockgrpc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

// The settings of a gRPC mock server are the grpc section of the settings the vlatte command reads, in its config
// file, VLATTE_* environment variables and flags: grpc.addr, VLATTE_GRPC_ADDR, --grpc-addr. HTTP mock servers read
// the same sources, and leave the grpc section to this one.

const (
	// DefaultConfigFileName is the base name of the config file DefaultConfigSources reads, the HTTP server's.
	DefaultConfigFileName = ".vlatterc"

	// EnvPrefix starts the names of the environment variables of the settings.
	EnvPrefix = "VLATTE"

	// _keyConfigFile names the config file to read, as --config or VLATTE_CONFIG: the HTTP server's setting, which
	// this server reads too.
	_keyConfigFile = "config"
)

// DefaultConfigDirectories are where DefaultConfigSources looks for the config file, in order.
var DefaultConfigDirectories = []string{".", "testdata"}

type (
	// ConfigSource is a source of server settings, such as a file, the environment or arguments.
	ConfigSource = config.Source

	// ConfigFlag describes the command-line flag of a setting.
	ConfigFlag = config.Flag

	field       = config.Field[Config]
	recordField = config.Field[RecordConfig]
)

// _schema declares every setting of a gRPC mock server: the grpc section, and config, which names the file.
var _schema = sync.OnceValue(func() *config.Schema[Config] {
	d := defaultConfig()

	return config.NewSchema(inSection(withRecord(
		field{Key: _keyConfigFile, Kind: config.String, Usage: "Config file to read, in place of " + DefaultConfigFileName + ": the HTTP server's, whose grpc section this is."},
		field{
			Key: "grpc", Kind: config.Bool, Usage: "Serve gRPC mocks: the vlatte command serves them when the grpc section is on.",
			Set: func(c *Config, v config.Value) error { c.Serve = v.Bool; return nil },
		},
		field{
			Key: "grpc.name", Kind: config.String, Usage: "Name of the gRPC server, in its log lines.",
			Set: func(c *Config, v config.Value) error { c.Name = v.Str; return nil },
		},
		field{
			Key: "grpc.addr", Kind: config.String, Default: d.Addr, Usage: "Address the gRPC server listens on: host:port or :port.",
			Set: func(c *Config, v config.Value) error { c.Addr = v.Str; return nil },
		},
		field{
			Key: "grpc.root_dir", Kind: config.String, Usage: "Directory relative mock_files patterns start at. Empty is the working directory.",
			Set: func(c *Config, v config.Value) error { c.RootDir = v.Str; return nil },
		},
		field{
			Key: "grpc.mock_files", Kind: config.List, Usage: "Glob patterns of gRPC mock files, loaded at start.",
			Set: func(c *Config, v config.Value) error { c.MockFiles = v.List; return nil },
		},
		field{
			Key: "grpc.descriptors", Kind: config.List,
			Usage: "Descriptor set files of the services to mock, such as protoc --descriptor_set_out --include_imports writes.",
			Set:   func(c *Config, v config.Value) error { c.Descriptors = v.List; return nil },
		},
		field{
			Key: "grpc.reflection", Kind: config.Bool, Usage: "Serve gRPC server reflection, so grpcurl and other tools list the services.",
			Set: func(c *Config, v config.Value) error { c.Reflection = v.Bool; return nil },
		},
		field{
			Key: "grpc.template.env", Kind: config.List,
			Usage: "Environment variables templates may read with env, by exact name. Templates read none without it.",
			Set: func(c *Config, v config.Value) error {
				c.TemplateEnv = v.List
				_, err := vlatte.NewGoTemplateEngine(vlatte.WithEnv(v.List...))

				return err
			},
		},
		field{
			Key: "grpc.tls", Kind: config.Bool, Usage: "Serve TLS. Only the vlatte command reads it: in Go, Server.StartTLS serves TLS.",
			Set: func(c *Config, v config.Value) error { c.UseTLS = v.Bool; return nil },
		},
		field{
			Key: "grpc.tls.cert", Kind: config.String, Usage: "TLS certificate file. Requires grpc.tls.key. Without one, TLS serves a self-signed certificate.",
			Set: turnOnTLS,
		},
		field{Key: "grpc.tls.key", Kind: config.String, Usage: "TLS private key file. Requires grpc.tls.cert.", Set: turnOnTLS},
		field{
			Key: "grpc.tls.ca", Kind: config.String,
			Usage: "File of the CA certificates that client certificates must be signed by (mutual TLS). Requires grpc.tls.cert and grpc.tls.key.",
			Set:   turnOnTLS,
		},
		field{
			Key: "grpc.no_match_code", Kind: config.String, Default: "NOT_FOUND",
			Usage: "Code of the answer to a call no mock matched: its name, such as UNIMPLEMENTED, or its number.",
			Set: func(c *Config, v config.Value) error {
				var raw any = v.Str
				if n, err := strconv.ParseInt(v.Str, 10, 64); err == nil {
					raw = n
				}

				code, err := codeOf(config.Value{Key: v.Key, Origin: v.Origin, Any: raw})
				c.NoMatchCode = code

				return err
			},
		},
		field{
			Key: "grpc.debug_headers", Kind: config.Bool, Default: "true",
			Usage: "Add the debug header metadata, such as vlatte-matched-mock-id, to the server's answers.",
			Set:   func(c *Config, v config.Value) error { c.NoDebugHeaders = !v.Bool; return nil },
		},
		field{
			Key: "grpc.near_misses", Kind: config.Bool, Default: "true",
			Usage: "Journal the mocks closest to a call no mock matched.",
			Set:   func(c *Config, v config.Value) error { c.NoNearMisses = !v.Bool; return nil },
		},
		field{
			Key: "grpc.shutdown_timeout", Kind: config.Duration, Default: _defaultShutdownTimeout.String(),
			Usage: "How long the server waits for the calls in flight when it stops: milliseconds or a Go duration (1.5s).",
			Set: func(c *Config, v config.Value) error {
				if v.Dur <= 0 {
					return errors.New("expected more than 0")
				}

				c.ShutdownTimeout = v.Dur

				return nil
			},
		},
		field{
			Key: "grpc.log.level", Kind: config.Int, Choices: config.LogLevels, Default: "info", Usage: "Level of the gRPC server's log.",
			Set: func(c *Config, v config.Value) error { c.LogLevel = int8(v.Int); return nil },
		},
		// The vlatte command's settings, which it reads from the same sources: a server accepts them, and ignores
		// them, so a config file it shares with the command loads.
		field{Key: "grpc.admin", Kind: config.Bool, Hidden: true},
		field{Key: "grpc.admin.addr", Kind: config.String, Hidden: true},
		field{
			Key: "grpc.journal", Kind: config.Bool, Usage: "Keep a journal of the calls the gRPC server serves, of every outcome.",
			Set: func(c *Config, v config.Value) error {
				config.Toggle(&c.Journal, defaultJournalConfig(), v.Bool)
				return nil
			},
		},
		field{
			Key: "grpc.journal.dir", Kind: config.String,
			Usage: "Directory of the journal's files, kept across restarts. Empty keeps them in a temporary directory, removed when the server stops.",
			Set: func(c *Config, v config.Value) error {
				config.Section(&c.Journal, defaultJournalConfig()).Dir = v.Str
				return nil
			},
		},
		field{
			Key: "grpc.journal.max_entries", Kind: config.Int, Default: strconv.Itoa(journal.DefaultMaxEntries),
			Usage: "How many calls the journal keeps, the newest. 0 keeps them all.",
			Set: func(c *Config, v config.Value) error {
				config.Section(&c.Journal, defaultJournalConfig()).MaxEntries = int(v.Int)
				return nil
			},
		},
		field{
			Key: "grpc.journal.max_body_size", Kind: config.Int, Default: strconv.Itoa(journal.DefaultMaxBodySize),
			Usage: "How many bytes of a call's request, as JSON, the journal keeps. 0 keeps whole requests.",
			Set: func(c *Config, v config.Value) error {
				config.Section(&c.Journal, defaultJournalConfig()).MaxBodySize = v.Int
				return nil
			},
		},
		field{
			Key: "grpc.record", Kind: config.Bool, Usage: "Record the calls the gRPC server proxies as mock files, from start to close.",
			Set: func(c *Config, v config.Value) error {
				config.Toggle(&c.Record, *defaultRecordConfig(), v.Bool)
				return nil
			},
		},
	)...)...).Only("grpc", _keyConfigFile)
})

// _recordRepeats are the names of what a recording keeps of a request that comes again, the default first.
var _recordRepeats = []config.Choice{
	{Name: "sequence", Value: int64(vlatte.RecordSequence)},
	{Name: "first", Value: int64(vlatte.RecordFirst)},
}

// _recordSchema declares the settings of a recording, as the grpc.record section of the server's settings has them:
// the body of the admin API's POST /recording.
var _recordSchema = sync.OnceValue(func() *config.Schema[RecordConfig] {
	return config.NewSchema(recordFields()...)
})

// recordFields declares the settings of a recording: the keys of the grpc.record section of the server's settings,
// and of the body of the admin API's POST /recording.
func recordFields() []recordField {
	rec := defaultRecordConfig()

	return []recordField{
		{
			Key: "dir", Kind: config.String, Default: rec.Dir, Usage: "Directory of the gRPC recordings. Relative paths start at grpc.root_dir.",
			Set: func(r *RecordConfig, v config.Value) error { r.Dir = v.Str; return nil },
		},
		{
			Key: "format", Kind: config.String, Default: rec.Format,
			Usage: "Format of the recorded gRPC mock files: " + strings.Join(MockFileFormats(), ", ") + ".",
			Set: func(r *RecordConfig, v config.Value) error {
				format, ok := config.Format(v.Str)
				if !ok {
					return fmt.Errorf("expected one of %s", strings.Join(MockFileFormats(), ", "))
				}

				r.Format = format

				return nil
			},
		},
		{
			Key: "target", Kind: config.String,
			Usage: "gRPC server to record, such as localhost:50051: the calls the recording takes go to it, without TLS, before any mock.",
			Set:   func(r *RecordConfig, v config.Value) error { r.Target = v.Str; return nil },
		},
		{
			Key: "methods", Kind: config.List,
			Usage: "Methods of the calls the recording takes, by whole segments of their full method, such as Service/Method. Empty takes every method.",
			Set:   func(r *RecordConfig, v config.Value) error { r.Methods = v.List; return nil },
		},
		{
			Key: "request_headers", Kind: config.List, Usage: "Metadata keys recordings match: the value each had, or its absence.",
			Set: func(r *RecordConfig, v config.Value) error { r.RequestHeaders = v.List; return nil },
		},
		{
			Key: "repeats", Kind: config.Int, Choices: _recordRepeats, Default: _recordRepeats[0].Name,
			Usage: "What a recording keeps of a request that comes again: every answer, in order, or the first one.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Repeats = vlatte.RecordRepeats(v.Int)
				return nil
			},
		},
		{
			Key: "include_mocked", Kind: config.Bool, Usage: "Record the answers of mocks too, not only proxied ones.",
			Set: func(r *RecordConfig, v config.Value) error { r.IncludeMocked = v.Bool; return nil },
		},
	}
}

// withRecord returns fields, then the settings of a recording under the server's grpc.record section.
func withRecord(fields ...field) []field {
	settings := recordFields()

	records := make([]field, len(settings))
	for i, f := range settings {
		set := f.Set
		records[i] = field{
			Key: "grpc.record." + f.Key, Usage: f.Usage, Default: f.Default, Kind: f.Kind, Choices: f.Choices,
			Set: func(c *Config, v config.Value) error {
				return set(config.Section(&c.Record, *defaultRecordConfig()), v)
			},
		}
	}

	return slices.Concat(fields, records)
}

// inSection makes setting a key of the grpc section turn it on, as setting the grpc key does.
func inSection(fields ...field) []field {
	for i, f := range fields {
		if set := f.Set; set != nil && strings.HasPrefix(f.Key, "grpc.") {
			fields[i].Set = func(c *Config, v config.Value) error {
				c.Serve = true
				return set(c, v)
			}
		}
	}

	return fields
}

// turnOnTLS turns TLS on for a setting of its files: loadConfigSources loads them.
func turnOnTLS(c *Config, _ config.Value) error {
	c.UseTLS = true
	return nil
}

// ConfigFile reads server settings from a JSON or YAML file: those of its grpc section. With dirs, path is a base
// name, such as DefaultConfigFileName, looked up in each dir; finding none is fine. A --config flag or VLATTE_CONFIG
// variable from a later source names the file to read instead.
func ConfigFile(path string, dirs ...string) ConfigSource {
	return config.File(_keyConfigFile, path, dirs...)
}

// ConfigEnv reads server settings from VLATTE_GRPC_* environment variables, such as VLATTE_GRPC_ADDR for grpc.addr,
// and VLATTE_CONFIG. The other VLATTE_* variables are the HTTP server's: a VLATTE_GRPC_* variable that is no setting
// is an error.
func ConfigEnv() ConfigSource {
	return config.Env(EnvPrefix)
}

// ConfigArgs reads server settings from command-line arguments, such as --grpc-addr=:50051. Every argument must be
// one of them: the vlatte command splits its arguments between its servers by their flags, see ConfigFlags.
func ConfigArgs(args []string) ConfigSource {
	return config.Args(args)
}

// DefaultConfigSources lists the sources a server reads when Config.ConfigSources is nil, lowest precedence first:
// the config file, VLATTE_GRPC_* environment variables, then args.
func DefaultConfigSources(args []string) []ConfigSource {
	return []ConfigSource{ConfigFile(DefaultConfigFileName, DefaultConfigDirectories...), ConfigEnv(), ConfigArgs(args)}
}

// ConfigUsage describes every server setting: its flag, environment variable, file key, allowed values and default.
func ConfigUsage() string {
	return _schema().Usage(EnvPrefix)
}

// ConfigFlags describes the command-line flag of every server setting, for example to register shell completions.
func ConfigFlags() []ConfigFlag {
	return _schema().Flags()
}

// loadConfigSources applies the settings of conf.ConfigSources to conf. It returns flag.ErrHelp when the arguments ask
// for help, or a *config.Error listing every problem found.
func loadConfigSources(conf *Config) error {
	sources := conf.ConfigSources
	if sources == nil {
		sources = DefaultConfigSources(nil)
	}

	if len(sources) == 0 {
		return nil
	}

	// Sources change sections in place: copy them, so one shared with other servers is never changed.
	if conf.Journal != nil {
		j := *conf.Journal
		conf.Journal = &j
	}

	if conf.Record != nil {
		conf.Record = conf.Record.clone()
	}

	values, err := _schema().Load(conf, sources)

	var problems []config.Problem
	if err != nil {
		cfgErr, ok := errors.AsType[*config.Error](err)
		if !ok {
			return err
		}

		problems = cfgErr.Problems
	}

	loaded, tlsProblems := config.LoadTLS(values, "grpc.tls.")
	if loaded.Certificates != nil {
		conf.TLSCertificates = loaded.Certificates
	}

	if loaded.ClientCAs != nil {
		conf.TLSClientCAs = loaded.ClientCAs
	}

	if problems = append(problems, tlsProblems...); len(problems) > 0 {
		return &config.Error{Problems: problems}
	}

	return nil
}

// defaultJournalConfig is the journal a source turns on.
func defaultJournalConfig() JournalConfig {
	return JournalConfig{MaxEntries: journal.DefaultMaxEntries, MaxBodySize: journal.DefaultMaxBodySize}
}
