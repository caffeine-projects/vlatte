package mockhttp

import (
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/journal"
)

const (
	// DefaultConfigFileName is the base name of the config file looked up by default:
	// .vlatterc.json, .vlatterc.yaml or .vlatterc.yml.
	DefaultConfigFileName = ".vlatterc"
	// EnvPrefix starts the names of the environment variables that configure a server, such as VLATTE_ADDR.
	EnvPrefix = "VLATTE"
)

// DefaultConfigDirectories are the directories searched for the config file, in order.
var DefaultConfigDirectories = []string{".", "testdata"}

// _keyConfigFile names the config file to load, as in --config or VLATTE_CONFIG.
const _keyConfigFile = "config"

// ConfigFile reads server settings from a JSON or YAML file, chosen by its extension: .json, .yaml or .yml.
//
// With no dirs, path is the file to read, and it must exist.
// With dirs, path is a base name, such as DefaultConfigFileName, looked up in each dir; finding none is fine.
// A --config flag or VLATTE_CONFIG variable from a later source names the file to read instead of the lookup.
func ConfigFile(path string, dirs ...string) ConfigSource {
	return config.File(_keyConfigFile, path, dirs...)
}

// ConfigEnv reads server settings from VLATTE_* environment variables, such as VLATTE_LOG_LEVEL for log.level.
// Any other VLATTE_* variable is an error, except VLATTE_DOCKER_HOST, which the vlatte command reads.
func ConfigEnv() ConfigSource {
	return config.Env(EnvPrefix, EnvPrefix+"_DOCKER_HOST")
}

// ConfigArgs reads server settings from command-line arguments, such as --log-level=debug.
// It never reads os.Args: pass the arguments, such as os.Args[1:].
func ConfigArgs(args []string) ConfigSource {
	return config.Args(args)
}

// DefaultConfigSources lists the sources a server reads when Config.ConfigSources is nil, lowest precedence first:
// the config file, VLATTE_* environment variables, then args.
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

// loadConfigSources applies the settings of conf.ConfigSources to conf.
// It returns flag.ErrHelp when the arguments ask for help, or a *config.Error listing every problem found.
func loadConfigSources(conf *Config) error {
	sources := conf.ConfigSources
	if sources == nil {
		sources = DefaultConfigSources(nil)
	}

	if len(sources) == 0 {
		return nil
	}

	// Sources change sections in place. Copy them, so a struct shared with other servers,
	// such as DefaultCORSConfig passed by reference, is never modified.
	if conf.CORS != nil {
		cors := *conf.CORS
		conf.CORS = &cors
	}

	if conf.Proxy != nil {
		proxy := *conf.Proxy
		conf.Proxy = &proxy
	}

	if conf.Record != nil {
		record := *conf.Record
		conf.Record = &record
	}

	if conf.Admin != nil {
		admin := *conf.Admin
		conf.Admin = &admin
	}

	if conf.Journal != nil {
		journal := *conf.Journal
		conf.Journal = &journal
	}

	if conf.proxyAll != nil {
		proxyAll := *conf.proxyAll
		proxyAll.requestHeader = slices.Clone(proxyAll.requestHeader)
		proxyAll.header = slices.Clone(proxyAll.header)
		proxyAll.removeRequestHeaders = slices.Clone(proxyAll.removeRequestHeaders)
		conf.proxyAll = &proxyAll
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

	problems = append(problems, loadTLSFiles(conf, values)...)
	problems = append(problems, loadProxyCA(conf, values)...)
	problems = append(problems, checkProxyAll(conf, values)...)
	problems = append(problems, checkName(conf, values)...)

	if len(problems) > 0 {
		return &config.Error{Problems: problems}
	}

	return nil
}

// loadTLSFiles loads the TLS files that sources named. Code sets them with ConfigBuilder.TLSCertKeyPair or TLSMutual.
func loadTLSFiles(conf *Config, values map[string]config.Value) []config.Problem {
	cert, hasCert := values["tls.cert"]
	key, hasKey := values["tls.key"]
	ca, hasCA := values["tls.ca"]

	switch {
	case !hasCert && !hasKey && !hasCA:
		return nil
	case hasCert && !hasKey:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: cert.Origin, Key: "tls.key",
			Message: "tls.cert is set but tls.key is not", Hint: "set both",
		}}
	case hasKey && !hasCert:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: key.Origin, Key: "tls.cert",
			Message: "tls.key is set but tls.cert is not", Hint: "set both",
		}}
	case !hasCert:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: ca.Origin, Key: "tls.cert",
			Message: "tls.ca is set but tls.cert and tls.key are not", Hint: "mutual TLS needs the server's certificate and key too",
		}}
	}

	var problems []config.Problem
	if err := applyTLS(conf, cert.Str, key.Str, ""); err != nil {
		problems = append(problems, tlsFileProblem(cert, "can't load the certificate and key", err))
	}

	if hasCA {
		pool, err := loadCertPool(ca.Str)
		if err != nil {
			return append(problems, tlsFileProblem(ca, "can't load the client CAs", err))
		}

		conf.TLSClientCAs = pool
	}

	return problems
}

// tlsFileProblem reports a TLS file that can't be loaded as a problem of the setting that named it.
func tlsFileProblem(v config.Value, what string, err error) config.Problem {
	p := config.Problem{
		Code: config.CodeInvalidValue, Origin: v.Origin, Key: v.Key, Value: v.Str,
		Message: what + ": " + err.Error(),
	}

	if wd, err := os.Getwd(); err == nil && !filepath.IsAbs(v.Str) {
		p.Hint = "relative paths start at " + wd
	}

	return p
}

type (
	field       = config.Field[Config]
	recordField = config.Field[RecordConfig]
)

var _logLevels = []config.Choice{
	{Name: "trace", Value: int64(zerolog.TraceLevel)},
	{Name: "debug", Value: int64(zerolog.DebugLevel)},
	{Name: "info", Value: int64(zerolog.InfoLevel)},
	{Name: "warn", Value: int64(zerolog.WarnLevel)},
	{Name: "error", Value: int64(zerolog.ErrorLevel)},
	{Name: "fatal", Value: int64(zerolog.FatalLevel)},
	{Name: "panic", Value: int64(zerolog.PanicLevel)},
	{Name: "none", Value: int64(zerolog.NoLevel)},
	{Name: "disabled", Value: int64(zerolog.Disabled)},
}

var _recordRepeats = []config.Choice{
	{Name: "sequence", Value: int64(vlatte.RecordSequence)},
	{Name: "first", Value: int64(vlatte.RecordFirst)},
}

var _logVerbosities = []config.Choice{
	{Name: vlatte.LogBasic.String(), Value: int64(vlatte.LogBasic)},
	{Name: vlatte.LogHeader.String(), Value: int64(vlatte.LogHeader)},
	{Name: vlatte.LogBody.String(), Value: int64(vlatte.LogBody)},
}

// _schema declares every server setting. Defaults shown in help come from the Go defaults, which stay the only ones.
var _schema = sync.OnceValue(func() *config.Schema[Config] {
	d := defaultConfig()

	before := []field{
		{
			Key: _keyConfigFile, Short: "c", Kind: config.String,
			Usage: fmt.Sprintf("Config file to load, instead of looking up %s.{json,yaml,yml} in %s.",
				DefaultConfigFileName, strings.Join(DefaultConfigDirectories, " and ")),
		},
		{
			Key: "name", Short: "n", Kind: config.String,
			Usage: "Server name, shown in logs. It names the folder of the server's files in " + DefaultWorkDir +
				", unless workdir is set: a path inside it, which doesn't start with _.",
			Set: func(c *Config, v config.Value) error { c.Name = v.Str; return nil },
		},
		{
			Key: "addr", Short: "a", Kind: config.String,
			Usage: "Address to listen on: host:port, :port or port. Empty picks a random local port.",
			Set:   func(c *Config, v config.Value) error { c.Addr = v.Str; return checkAddr(v.Str) },
		},
		{
			Key: "root_dir", Kind: config.String, Usage: "Directory that relative mock file patterns and recording paths start at.",
			Set: func(c *Config, v config.Value) error { c.RootDir = v.Str; return nil },
		},
		{
			Key: "workdir", Kind: config.String, Default: DefaultWorkDir + ", or " + DefaultWorkDir + "/<name> for a named server",
			Usage: "Directory of the server's own files: its mock files, recordings, admin mocks and journal. A path " +
				"setting that starts with " + WorkDirVar + " starts there. Relative paths start at root_dir.",
			Set: func(c *Config, v config.Value) error { c.WorkDir = v.Str; return nil },
		},
		{
			Key: "mock_files", Kind: config.List, Default: strings.Join(d.MockFileSearchPatterns, ","),
			Usage: "Glob patterns of the mock files to load on start.",
			Set:   func(c *Config, v config.Value) error { c.MockFileSearchPatterns = v.List; return nil },
		},
		{
			Key: "no_match_status_code", Kind: config.Int, Default: strconv.Itoa(d.RequestWasNotMatchedStatusCode),
			Usage: "Status code of the response to a request that matches no mock.",
			Set: func(c *Config, v config.Value) error {
				c.RequestWasNotMatchedStatusCode = int(v.Int)
				return checkStatus(v.Int)
			},
		},
		{
			Key: "error_status_code", Kind: config.Int, Default: strconv.Itoa(d.ErrorStatusCode),
			Usage: "Status code of the response when serving a request fails, such as a reply that fails.",
			Set: func(c *Config, v config.Value) error {
				c.ErrorStatusCode = int(v.Int)
				return checkStatus(v.Int)
			},
		},
		{
			Key: "https", Kind: config.Bool, Usage: "Serve HTTPS instead of HTTP. Only the vlatte command reads it.",
			Set: func(c *Config, v config.Value) error { c.UseHTTPS = v.Bool; return nil },
		},
		{
			Key: "colors", Kind: config.Bool, Default: strconv.FormatBool(d.Colors), Usage: "Use colors in debug logs.",
			Set: func(c *Config, v config.Value) error { c.Colors = v.Bool; return nil },
		},
		{
			Key: "debug", Kind: config.Bool, Usage: "Print descriptive logs for each request, instead of structured ones.",
			Set: func(c *Config, v config.Value) error { c.Debug = v.Bool; return nil },
		},
		{
			Key: "debug_headers", Kind: config.Bool, Default: strconv.FormatBool(!d.NoDebugHeaders),
			Usage: "Add debug headers, such as VLatte-Matched-Mock-Id, to each answer. VLatte-Error stays either way.",
			Set:   func(c *Config, v config.Value) error { c.NoDebugHeaders = !v.Bool; return nil },
		},
		{
			Key: "near_misses", Kind: config.Bool, Default: strconv.FormatBool(!d.NoNearMisses),
			Usage: "List the mocks closest to a request no mock matched in its answer. It runs the matchers matching never reached.",
			Set:   func(c *Config, v config.Value) error { c.NoNearMisses = !v.Bool; return nil },
		},
		{Key: "tls.cert", Kind: config.String, Usage: "TLS certificate file. Requires tls.key."},
		{Key: "tls.key", Kind: config.String, Usage: "TLS private key file. Requires tls.cert."},
		{
			Key: "tls.ca", Kind: config.String,
			Usage: "File of the CA certificates that client certificates must be signed by (mutual TLS). Requires tls.cert and tls.key.",
		},
		{
			Key: "log.level", Kind: config.Int, Choices: _logLevels, Default: choiceName(_logLevels, int64(d.LogLevel)),
			Usage: "Log level.",
			Set:   func(c *Config, v config.Value) error { c.LogLevel = vlatte.LogLevel(v.Int); return nil },
		},
		{
			Key: "log.verbosity", Kind: config.Int, Choices: _logVerbosities, Default: choiceName(_logVerbosities, int64(d.LogVerbosity)),
			Usage: "How much of each request and response to log.",
			Set:   func(c *Config, v config.Value) error { c.LogVerbosity = vlatte.LogVerbosity(v.Int); return nil },
		},
		{
			Key: "log.max_body_size", Kind: config.Int, Usage: "Bodies larger than this many bytes aren't logged. 0 logs bodies of any size.",
			Set: func(c *Config, v config.Value) error { c.LogBodyMaxSize = v.Int; return checkMin(v.Int, 0) },
		},
		{
			Key: "log.pretty", Kind: config.Bool, Default: strconv.FormatBool(d.LogPretty), Usage: "Human-friendly console logs instead of JSON.",
			Set: func(c *Config, v config.Value) error { c.LogPretty = v.Bool; return nil },
		},
		{
			Key: "cors", Kind: config.Bool, Usage: "Enable CORS.",
			Set: func(c *Config, v config.Value) error { toggle(&c.CORS, DefaultCORSConfig, v.Bool); return nil },
		},
		{
			Key: "cors.allowed_origin", Kind: config.String, Default: DefaultCORSConfig.AllowedOrigin,
			Usage: "Allowed origins, comma-separated. * allows any.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).AllowedOrigin = v.Str
				return nil
			},
		},
		{
			Key: "cors.allow_credentials", Kind: config.Bool,
			Usage: "Send Access-Control-Allow-Credentials: true. With *, the origin of the request is allowed.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).AllowCredentials = v.Bool
				return nil
			},
		},
		{
			Key: "cors.allowed_methods", Kind: config.String, Default: DefaultCORSConfig.AllowedMethods,
			Usage: "Allowed methods, comma-separated. * allows the method a preflight request asks for.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).AllowedMethods = v.Str
				return nil
			},
		},
		{
			Key: "cors.allowed_headers", Kind: config.String,
			Usage: "Allowed headers, comma-separated. Empty or * allows the headers a preflight request asks for.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).AllowedHeaders = v.Str
				return nil
			},
		},
		{
			Key: "cors.expose_headers", Kind: config.String, Usage: "Headers exposed to browsers, comma-separated.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).ExposeHeaders = v.Str
				return nil
			},
		},
		{
			Key: "cors.max_age", Kind: config.Int, Default: strconv.Itoa(DefaultCORSConfig.MaxAge),
			Usage: "Seconds browsers may cache a preflight response. -1 omits Access-Control-Max-Age.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).MaxAge = int(v.Int)
				return checkMin(v.Int, -1)
			},
		},
		{
			Key: "cors.success_status_code", Kind: config.Int, Default: strconv.Itoa(DefaultCORSConfig.SuccessStatusCode),
			Usage: "Status code of the response to a preflight request. Browsers fail a preflight answered with a status other than 2xx.",
			Set: func(c *Config, v config.Value) error {
				section(&c.CORS, DefaultCORSConfig).SuccessStatusCode = int(v.Int)
				return checkStatus(v.Int)
			},
		},
		{
			Key: "proxy", Kind: config.Bool, Usage: "Proxy the requests that match no mock to their original host.",
			Set: func(c *Config, v config.Value) error { toggle(&c.Proxy, _defaultProxyConfig, v.Bool); return nil },
		},
		{
			Key: "proxy.via", Kind: config.String, Usage: "URL of a proxy to send proxied requests through.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Proxy, _defaultProxyConfig).Via = v.Str
				return checkProxyURL(v.Str)
			},
		},
		{
			Key: "proxy.timeout", Kind: config.Duration, Default: _defaultProxyConfig.Timeout.String(),
			Usage: "How long to wait for the response headers of a proxied request: milliseconds or a Go duration (1.5s). 0 waits forever.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Proxy, _defaultProxyConfig).Timeout = v.Dur
				return nil
			},
		},
		{
			Key: "proxy.ssl_verify", Kind: config.Bool, Usage: "Verify the TLS certificates of proxied servers.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Proxy, _defaultProxyConfig).SSLVerify = v.Bool
				return nil
			},
		},
		{
			Key: "proxy.intercept", Kind: config.Bool,
			Usage: "Serve the HTTPS of CONNECT tunnels with certificates signed by proxy.ca_cert, so mocks answer it. " +
				"Requires proxy.ca_cert and proxy.ca_key.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Proxy, _defaultProxyConfig).Intercept = v.Bool
				return nil
			},
		},
		{
			Key: "proxy.ca_cert", Kind: config.String,
			Usage: "Certificate file of the CA that signs the certificates proxy.intercept serves, which clients must trust. Requires proxy.ca_key.",
			Set:   func(c *Config, _ config.Value) error { section(&c.Proxy, _defaultProxyConfig); return nil },
		},
		{
			Key: "proxy.ca_key", Kind: config.String, Usage: "Private key file of proxy.ca_cert. Requires proxy.ca_cert.",
			Set: func(c *Config, _ config.Value) error { section(&c.Proxy, _defaultProxyConfig); return nil },
		},
		{
			Key: "record", Kind: config.Bool, Usage: "Record proxied requests and their responses as mock files, from start to close.",
			Set: func(c *Config, v config.Value) error { toggle(&c.Record, *defaultRecordConfig(), v.Bool); return nil },
		},
	}

	after := []field{
		{
			Key: "admin", Kind: config.Bool, Usage: "Serve the admin API, on an HTTP server of its own.",
			Set: func(c *Config, v config.Value) error { toggle(&c.Admin, AdminConfig{}, v.Bool); return nil },
		},
		{
			Key: "admin.addr", Kind: config.String,
			Usage: "Address the admin API listens on: host:port, :port or port. Empty picks a random local port.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Admin, AdminConfig{}).Addr = v.Str
				return checkAddr(v.Str)
			},
		},
		{
			Key: "admin.dir", Kind: config.String,
			Usage: "Directory of the files that keep the mocks the admin API adds across restarts, which only the server " +
				"loads: keep it out of mock_files. Relative paths start at root_dir, and {workdir} at workdir. Empty keeps them " +
				"in memory.",
			Set: func(c *Config, v config.Value) error {
				admin := section(&c.Admin, AdminConfig{})

				admin.Store = nil
				if v.Str != "" {
					admin.Store = FileStore(v.Str)
				}

				return nil
			},
		},
		{
			Key: "journal", Kind: config.Bool, Usage: "Keep a journal of the requests the server serves, of every outcome.",
			Set: func(c *Config, v config.Value) error { toggle(&c.Journal, defaultJournalConfig(), v.Bool); return nil },
		},
		{
			Key: "journal.dir", Kind: config.String,
			Usage: "Directory of the journal's files, kept across restarts. Relative paths start at root_dir, and " +
				"{workdir} at workdir. Empty keeps them in a temporary directory, removed when the server stops.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Journal, defaultJournalConfig()).Dir = v.Str
				return nil
			},
		},
		{
			Key: "journal.max_entries", Kind: config.Int, Default: strconv.Itoa(journal.DefaultMaxEntries),
			Usage: "How many requests the journal keeps, the newest. 0 keeps them all.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Journal, defaultJournalConfig()).MaxEntries = int(v.Int)
				return checkMin(v.Int, 0)
			},
		},
		{
			Key: "journal.max_body_size", Kind: config.Int, Default: strconv.Itoa(journal.DefaultMaxBodySize),
			Usage: "How many bytes of a request's body the journal keeps. 0 keeps whole bodies.",
			Set: func(c *Config, v config.Value) error {
				section(&c.Journal, defaultJournalConfig()).MaxBodySize = v.Int
				return checkMin(v.Int, 0)
			},
		},
		{
			Key: "webhook.timeout", Kind: config.Duration,
			Usage: "How long a webhook without a timeout of its own waits for its answer: milliseconds or a Go duration " +
				"(1.5s). 0 uses " + httpx.DefaultWebhookTimeout.String() + ".",
			Set: func(c *Config, v config.Value) error { c.WebhookTimeout = v.Dur; return nil },
		},
	}

	records := recordFields()
	fields := make([]field, 0, len(before)+len(records)+len(after))
	fields = append(fields, before...)

	for _, f := range records {
		fields = append(fields, inRecord(f))
	}

	return config.NewSchema(withProxyAll(append(fields, after...)...)...)
})

// _recordSchema declares the settings of a recording, as the record section of the server's settings has them: the
// body of the admin API's POST /recording.
var _recordSchema = sync.OnceValue(func() *config.Schema[RecordConfig] {
	return config.NewSchema(recordFields()...)
})

// recordFields declares the settings of a recording: the keys of the record section of the server's settings, and of
// the body of the admin API's POST /recording.
func recordFields() []recordField {
	rec := defaultRecordConfig()

	return []recordField{
		{
			Key: "dir", Kind: config.String, Default: rec.Dir,
			Usage: "Directory of the recordings. Relative paths start at root_dir, and {workdir} at workdir.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Dir = v.Str
				return nil
			},
		},
		{
			Key: "format", Kind: config.String, Default: rec.Format,
			Usage: "Format of the recorded mock files: " + strings.Join(MockFileFormats(), ", ") + ".",
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
			Usage: "Server to record, such as https://api.example.com: the requests the recording takes go to it, before any mock.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Target = v.Str
				if v.Str == "" {
					return nil
				}

				return checkRecordTarget(v.Str)
			},
		},
		{
			Key: "skip_ssl_verify", Kind: config.Bool,
			Usage: "Skip the verification of the TLS certificate of record.target, such as for a self-signed one.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.SkipSSLVerify = v.Bool
				return nil
			},
		},
		{
			Key: "methods", Kind: config.List, Usage: "Methods of the requests the recording takes. Empty takes every method.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Methods = v.List
				return nil
			},
		},
		{
			Key: "paths", Kind: config.List,
			Usage: "Glob patterns of the paths of the requests the recording takes, such as /v1/*, where * matches / too. Empty takes every path.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Paths = v.List
				return nil
			},
		},
		{
			Key: "request_headers", Kind: config.List,
			Usage: "Request headers recordings match: the first value each had, or its absence.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.RequestHeaders = v.List
				return nil
			},
		},
		{
			Key: "exclude_response_headers", Kind: config.List, Default: strings.Join(rec.ExcludeResponseHeaders, ","),
			Usage: "Response headers recordings leave out. A list replaces the default; an empty one keeps every header.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.ExcludeResponseHeaders = v.List
				return nil
			},
		},
		{
			Key: "body_files", Kind: config.Bool, Usage: "Write each recorded response body to a file of its own, instead of inline.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.BodyFiles = v.Bool
				return nil
			},
		},
		{
			Key: "max_inline_body", Kind: config.Int, Default: strconv.FormatInt(rec.MaxInlineBody, 10),
			Usage: "Bytes of a body kept in memory while it is recorded: a larger one goes to a file as it streams. " +
				"Text bodies kept in memory are written inline. 0 uses the default.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.MaxInlineBody = v.Int
				return checkMin(v.Int, 0)
			},
		},
		{
			Key: "max_body_size", Kind: config.Int, Default: strconv.FormatInt(rec.MaxBodySize, 10),
			Usage: "Calls with a larger response body, in bytes, aren't recorded. 0 uses the default; a negative value means no limit.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.MaxBodySize = v.Int
				return nil
			},
		},
		{
			Key: "repeats", Kind: config.Int, Choices: _recordRepeats, Default: choiceName(_recordRepeats, int64(rec.Repeats)),
			Usage: "What a recording keeps of a request that comes again: every response, in order, or the first one.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.Repeats = vlatte.RecordRepeats(v.Int)
				return nil
			},
		},
		{
			Key: "include_mocked", Kind: config.Bool, Usage: "Record the answers of mocks too, not only proxied ones.",
			Set: func(r *RecordConfig, v config.Value) error {
				r.IncludeMocked = v.Bool
				return nil
			},
		},
	}
}

// inRecord declares a setting of a recording under the server's record section, which it turns on.
func inRecord(f recordField) field {
	set := f.Set

	return field{
		Key: "record." + f.Key, Short: f.Short, Usage: f.Usage, Default: f.Default, Kind: f.Kind, Choices: f.Choices,
		Set: func(c *Config, v config.Value) error { return set(section(&c.Record, *defaultRecordConfig()), v) },
	}
}

// loadProxyCA loads the CA of proxy.ca_cert and proxy.ca_key. Interception from the settings needs one: clients must
// trust the CA, and one the server makes lives only as long as the server.
func loadProxyCA(conf *Config, values map[string]config.Value) []config.Problem {
	// A source that turned the forward proxy off, such as --proxy=false, leaves it off.
	if conf.Proxy == nil {
		return nil
	}

	cert, hasCert := values["proxy.ca_cert"]
	key, hasKey := values["proxy.ca_key"]
	intercept, hasIntercept := values["proxy.intercept"]

	switch {
	case hasCert && !hasKey:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: cert.Origin, Key: "proxy.ca_key",
			Message: "proxy.ca_cert is set but proxy.ca_key is not", Hint: "set both",
		}}
	case hasKey && !hasCert:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: key.Origin, Key: "proxy.ca_cert",
			Message: "proxy.ca_key is set but proxy.ca_cert is not", Hint: "set both",
		}}
	case hasCert:
		ca, err := tls.LoadX509KeyPair(cert.Str, key.Str)
		if err != nil {
			return []config.Problem{tlsFileProblem(cert, "can't load the CA's certificate and key", err)}
		}

		conf.Proxy.CA = &ca
	case hasIntercept && intercept.Bool && conf.Proxy.CA == nil:
		return []config.Problem{{
			Code: config.CodeMissingValue, Origin: intercept.Origin, Key: "proxy.ca_cert",
			Message: "proxy.intercept is set but proxy.ca_cert and proxy.ca_key are not",
			Hint:    "clients must trust the CA that signs the certificates of the hosts it intercepts",
		}}
	}

	return nil
}

// checkProxyAll reports proxy_all set without its target, from the source of a key that set it.
// checkName checks the name sources set, which names the server's work directory unless workdir is set: workdir may
// come from a later source. Code's names are checked as the server is built.
func checkName(conf *Config, values map[string]config.Value) []config.Problem {
	v, ok := values["name"]
	if !ok {
		return nil
	}

	problem := conf.nameProblem()
	if problem == "" {
		return nil
	}

	return []config.Problem{{Code: config.CodeInvalidValue, Origin: v.Origin, Key: "name", Value: conf.Name, Message: problem}}
}

func checkProxyAll(conf *Config, values map[string]config.Value) []config.Problem {
	if conf.proxyAll == nil || conf.proxyAll.hasTarget {
		return nil
	}

	var origin string

	for _, key := range slices.Sorted(maps.Keys(values)) {
		if key == "proxy_all" || strings.HasPrefix(key, "proxy_all.") {
			origin = values[key].Origin
			break
		}
	}

	return []config.Problem{{
		Code: config.CodeMissingValue, Origin: origin, Key: "proxy_all.target",
		Message: "proxy_all is set but proxy_all.target is not", Hint: "set the URL to proxy the requests no mock matches to",
	}}
}

// withProxyAll returns fields with those of proxy_all after the forward proxy's: a section with the keys of a mock
// file's proxy, whose reply answers the requests no mock matches. The keys that take an object, request_header and
// header, are left out: flags and variables can't give one. Code mocks ProxyAll with them.
func withProxyAll(fields ...field) []field {
	proxyAll := []field{{
		Key: "proxy_all", Kind: config.Bool,
		Usage: "Proxy the requests no mock matches, but those the forward proxy sends on, to proxy_all.target.",
		Set:   func(c *Config, v config.Value) error { toggle(&c.proxyAll, proxyDef{}, v.Bool); return nil },
	}}

	for _, pf := range _proxyFields {
		if pf.Kind == config.Any {
			continue
		}

		proxyAll = append(proxyAll, field{
			Key: "proxy_all." + pf.Key, Kind: pf.Kind, Usage: pf.Usage, Default: pf.Default,
			Set: func(c *Config, v config.Value) error { return pf.Set(section(&c.proxyAll, proxyDef{}), v) },
		})
	}

	after := 0
	for i, f := range fields {
		if strings.HasPrefix(f.Key, "proxy.") {
			after = i + 1
		}
	}

	return slices.Insert(fields, after, proxyAll...)
}

// section returns *p, creating it as a copy of def when nil, so def itself is never modified.
func section[S any](p **S, def S) *S {
	if *p == nil {
		*p = &def
	}

	return *p
}

// toggle turns a section on, keeping its settings if it already is, or off.
func toggle[S any](p **S, def S, on bool) {
	if !on {
		*p = nil
		return
	}

	section(p, def)
}

func choiceName(choices []config.Choice, value int64) string {
	for _, choice := range choices {
		if choice.Value == value {
			return choice.Name
		}
	}

	return strconv.FormatInt(value, 10)
}

// checkAddr accepts the addresses the server can listen on: empty, a port, or host:port.
func checkAddr(addr string) error {
	if addr == "" {
		return nil
	}

	if _, err := strconv.Atoi(addr); err == nil {
		return nil
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return errors.New("expected host:port, :port or a port number")
	}

	return nil
}

func checkStatus(code int64) error {
	if code < 100 || code > 599 {
		return errors.New("expected an HTTP status code from 100 to 599")
	}

	return nil
}

func checkMin(n, least int64) error {
	if n < least {
		return fmt.Errorf("expected %d or more", least)
	}

	return nil
}

func checkProxyURL(via string) error {
	_, err := parseVia(via)
	return err
}
