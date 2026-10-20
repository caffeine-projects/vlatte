package mockhttp

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

const (
	_testCertFile       = "e2e/testdata/cert/cert.pem"
	_testKeyFile        = "e2e/testdata/cert/key.pem"
	_testClientCertFile = "e2e/testdata/cert/cert_client.pem"
)

// loadConfig builds a server from setup, reading only the given sources, and returns its config.
func loadConfig(t *testing.T, setup *ConfigBuilder, sources ...ConfigSource) (*Config, error) {
	t.Helper()

	m, err := New(setup.ConfigSources(sources...))
	if err != nil {
		return nil, err
	}

	return m.Config(), nil
}

func configProblems(t *testing.T, err error) []config.Problem {
	t.Helper()

	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "want *config.Error, got %v", err)

	return cfgErr.Problems
}

func writeConfigFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestConfigSources_EveryKeyHasAWorkingFlag(t *testing.T) {
	// B1: most multi-word flags used to be ignored. Each setting's flag must reach the config,
	// and this table must cover every flag, so a new key can't be added without a test.
	fromFile := writeConfigFile(t, t.TempDir(), "settings.json", `{"name": "from-file"}`)
	tlsArgs := []string{"--tls-cert", _testCertFile, "--tls-key", _testKeyFile}

	// proxy.intercept needs a CA.
	caCert, caKey := writeTestCA(t, t.TempDir())
	intercept := []string{"--proxy-intercept", "--proxy-ca-cert", caCert, "--proxy-ca-key", caKey}

	// proxy_all needs its target. A file sets it, and its flag turns it off.
	proxyAllFile := writeConfigFile(t, t.TempDir(), "proxy_all.json", `{"proxy_all": {"target": "https://api.example.com"}}`)
	withTarget := func(args ...string) []string {
		return append([]string{"--proxy-all-target", "https://api.example.com"}, args...)
	}

	tests := map[string]struct {
		args  []string
		check func(t *testing.T, c *Config)
	}{
		"config":   {[]string{"--config", fromFile}, func(t *testing.T, c *Config) { require.Equal(t, "from-file", c.Name) }},
		"name":     {[]string{"--name", "api"}, func(t *testing.T, c *Config) { require.Equal(t, "api", c.Name) }},
		"addr":     {[]string{"--addr", "127.0.0.1:0"}, func(t *testing.T, c *Config) { require.Equal(t, "127.0.0.1:0", c.Addr) }},
		"root-dir": {[]string{"--root-dir", "root"}, func(t *testing.T, c *Config) { require.Equal(t, "root", c.RootDir) }},
		"mock-files": {[]string{"--mock-files", "a/*.yaml,b/*.yaml"}, func(t *testing.T, c *Config) {
			require.Equal(t, []string{"a/*.yaml", "b/*.yaml"}, c.MockFileSearchPatterns)
		}},
		"no-match-status-code": {[]string{"--no-match-status-code", "404"}, func(t *testing.T, c *Config) { require.Equal(t, 404, c.RequestWasNotMatchedStatusCode) }},
		"error-status-code":    {[]string{"--error-status-code", "500"}, func(t *testing.T, c *Config) { require.Equal(t, 500, c.ErrorStatusCode) }},
		"https":                {[]string{"--https"}, func(t *testing.T, c *Config) { require.True(t, c.UseHTTPS) }},
		"colors":               {[]string{"--colors=false"}, func(t *testing.T, c *Config) { require.False(t, c.Colors) }},
		"debug":                {[]string{"--debug"}, func(t *testing.T, c *Config) { require.True(t, c.Debug) }},
		"debug-headers":        {[]string{"--debug-headers=false"}, func(t *testing.T, c *Config) { require.True(t, c.NoDebugHeaders) }},
		"tls-cert":             {tlsArgs, func(t *testing.T, c *Config) { require.Len(t, c.TLSCertificates, 1) }},
		"tls-key":              {tlsArgs, func(t *testing.T, c *Config) { require.Len(t, c.TLSCertificates, 1) }},
		"tls-ca":               {slices.Concat(tlsArgs, []string{"--tls-ca", _testClientCertFile}), func(t *testing.T, c *Config) { require.NotNil(t, c.TLSClientCAs) }},
		"log-level":            {[]string{"--log-level", "warn"}, func(t *testing.T, c *Config) { require.Equal(t, vlatte.LogLevelWarn, c.LogLevel) }},
		"log-verbosity":        {[]string{"--log-verbosity", "body"}, func(t *testing.T, c *Config) { require.Equal(t, vlatte.LogBody, c.LogVerbosity) }},
		"log-max-body-size":    {[]string{"--log-max-body-size", "1024"}, func(t *testing.T, c *Config) { require.Equal(t, int64(1024), c.LogBodyMaxSize) }},
		"log-pretty":           {[]string{"--log-pretty=false"}, func(t *testing.T, c *Config) { require.False(t, c.LogPretty) }},
		"cors":                 {[]string{"--cors"}, func(t *testing.T, c *Config) { require.Equal(t, &DefaultCORSConfig, c.CORS) }},
		"cors-allowed-origin":  {[]string{"--cors-allowed-origin", "https://a.org"}, func(t *testing.T, c *Config) { require.Equal(t, "https://a.org", c.CORS.AllowedOrigin) }},
		"cors-allow-credentials": {
			[]string{"--cors-allow-credentials"}, func(t *testing.T, c *Config) { require.True(t, c.CORS.AllowCredentials) },
		},
		"cors-allowed-methods": {[]string{"--cors-allowed-methods", "GET"}, func(t *testing.T, c *Config) { require.Equal(t, "GET", c.CORS.AllowedMethods) }},
		"cors-allowed-headers": {[]string{"--cors-allowed-headers", "X-A"}, func(t *testing.T, c *Config) { require.Equal(t, "X-A", c.CORS.AllowedHeaders) }},
		"cors-expose-headers":  {[]string{"--cors-expose-headers", "X-B"}, func(t *testing.T, c *Config) { require.Equal(t, "X-B", c.CORS.ExposeHeaders) }},
		"cors-max-age":         {[]string{"--cors-max-age", "60"}, func(t *testing.T, c *Config) { require.Equal(t, 60, c.CORS.MaxAge) }},
		"cors-success-status-code": {
			[]string{"--cors-success-status-code", "200"}, func(t *testing.T, c *Config) { require.Equal(t, 200, c.CORS.SuccessStatusCode) },
		},
		"proxy":            {[]string{"--proxy"}, func(t *testing.T, c *Config) { require.Equal(t, &_defaultProxyConfig, c.Proxy) }},
		"proxy-via":        {[]string{"--proxy-via", "http://proxy:3128"}, func(t *testing.T, c *Config) { require.Equal(t, "http://proxy:3128", c.Proxy.Via) }},
		"proxy-timeout":    {[]string{"--proxy-timeout", "1500"}, func(t *testing.T, c *Config) { require.Equal(t, 1500*time.Millisecond, c.Proxy.Timeout) }},
		"proxy-ssl-verify": {[]string{"--proxy-ssl-verify"}, func(t *testing.T, c *Config) { require.True(t, c.Proxy.SSLVerify) }},
		"proxy-all":        {[]string{"--config", proxyAllFile, "--proxy-all=false"}, func(t *testing.T, c *Config) { require.Nil(t, c.proxyAll) }},
		"proxy-all-target": {
			[]string{"--proxy-all-target", "https://api.example.com"}, func(t *testing.T, c *Config) { require.Equal(t, "https://api.example.com", c.proxyAll.target) },
		},
		"proxy-all-remove-request-headers": {
			withTarget("--proxy-all-remove-request-headers", "x-a,x-b"),
			func(t *testing.T, c *Config) {
				require.Equal(t, []string{"x-a", "x-b"}, c.proxyAll.removeRequestHeaders)
			},
		},
		"proxy-all-trim-prefix": {withTarget("--proxy-all-trim-prefix", "/api"), func(t *testing.T, c *Config) { require.Equal(t, "/api", c.proxyAll.trimPrefix) }},
		"proxy-all-trim-suffix": {withTarget("--proxy-all-trim-suffix", ".json"), func(t *testing.T, c *Config) { require.Equal(t, ".json", c.proxyAll.trimSuffix) }},
		"proxy-all-timeout": {
			withTarget("--proxy-all-timeout", "1.5s"), func(t *testing.T, c *Config) { require.Equal(t, 1500*time.Millisecond, c.proxyAll.timeout) },
		},
		"proxy-all-ssl-verify":    {withTarget("--proxy-all-ssl-verify"), func(t *testing.T, c *Config) { require.True(t, c.proxyAll.sslVerify) }},
		"proxy-all-x-forwarded":   {withTarget("--proxy-all-x-forwarded"), func(t *testing.T, c *Config) { require.True(t, c.proxyAll.xForwarded) }},
		"proxy-all-preserve-host": {withTarget("--proxy-all-preserve-host"), func(t *testing.T, c *Config) { require.True(t, c.proxyAll.preserveHost) }},
		"proxy-intercept":         {intercept, func(t *testing.T, c *Config) { require.True(t, c.Proxy.Intercept) }},
		"proxy-ca-cert":           {intercept, func(t *testing.T, c *Config) { require.NotNil(t, c.Proxy.CA) }},
		"proxy-ca-key":            {intercept, func(t *testing.T, c *Config) { require.NotNil(t, c.Proxy.CA.PrivateKey) }},
		"record":                  {[]string{"--record"}, func(t *testing.T, c *Config) { require.Equal(t, defaultRecordConfig(), c.Record) }},
		"record-dir":              {[]string{"--record-dir", "out"}, func(t *testing.T, c *Config) { require.Equal(t, "out", c.Record.Dir) }},
		"record-format":           {[]string{"--record-format", ".YAML"}, func(t *testing.T, c *Config) { require.Equal(t, "yaml", c.Record.Format) }},
		"record-target": {
			[]string{"--record-target", "https://api.example.com"},
			func(t *testing.T, c *Config) { require.Equal(t, "https://api.example.com", c.Record.Target) },
		},
		"record-skip-ssl-verify": {
			[]string{"--record-skip-ssl-verify"}, func(t *testing.T, c *Config) { require.True(t, c.Record.SkipSSLVerify) },
		},
		"record-methods": {
			[]string{"--record-methods", "GET,post"}, func(t *testing.T, c *Config) { require.Equal(t, []string{"GET", "post"}, c.Record.Methods) },
		},
		"record-paths": {
			[]string{"--record-paths", "/v1/*,/v2/*"}, func(t *testing.T, c *Config) { require.Equal(t, []string{"/v1/*", "/v2/*"}, c.Record.Paths) },
		},
		"record-request-headers": {
			[]string{"--record-request-headers", "accept"}, func(t *testing.T, c *Config) { require.Equal(t, []string{"accept"}, c.Record.RequestHeaders) },
		},
		"record-exclude-response-headers": {
			[]string{"--record-exclude-response-headers", "set-cookie"},
			func(t *testing.T, c *Config) {
				require.Equal(t, []string{"set-cookie"}, c.Record.ExcludeResponseHeaders)
			},
		},
		"record-body-files": {[]string{"--record-body-files"}, func(t *testing.T, c *Config) { require.True(t, c.Record.BodyFiles) }},
		"record-max-inline-body": {
			[]string{"--record-max-inline-body", "4096"}, func(t *testing.T, c *Config) { require.Equal(t, int64(4096), c.Record.MaxInlineBody) },
		},
		"record-max-body-size": {
			[]string{"--record-max-body-size", "-1"}, func(t *testing.T, c *Config) { require.Equal(t, int64(-1), c.Record.MaxBodySize) },
		},
		"record-repeats": {
			[]string{"--record-repeats", "first"}, func(t *testing.T, c *Config) { require.Equal(t, vlatte.RecordFirst, c.Record.Repeats) },
		},
		"record-include-mocked": {[]string{"--record-include-mocked"}, func(t *testing.T, c *Config) { require.True(t, c.Record.IncludeMocked) }},
		"webhook-timeout":       {[]string{"--webhook-timeout", "1.5s"}, func(t *testing.T, c *Config) { require.Equal(t, 1500*time.Millisecond, c.WebhookTimeout) }},
	}

	flags := ConfigFlags()
	require.Len(t, tests, len(flags), "every flag needs a row")

	for _, flag := range flags {
		tc, ok := tests[flag.Name]
		require.True(t, ok, "no row for --%s", flag.Name)

		t.Run(flag.Name, func(t *testing.T) {
			c, err := loadConfig(t, Setup(), ConfigFile(DefaultConfigFileName, t.TempDir()), ConfigArgs(tc.args))
			require.NoError(t, err)
			tc.check(t, c)
		})
	}
}

func TestConfigSources_LibraryNeverReadsProcessArgs(t *testing.T) {
	// B2: go test passes -test.* flags, so a library server must not parse os.Args.
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{"vlatte", "--name=from-os-args", "-h"}

	m, err := New()

	require.NoError(t, err)
	require.Empty(t, m.Config().Name)
}

func TestConfigSources_SectionToggles(t *testing.T) {
	// B3: false used to turn sections on, and VLATTE_PROXY=true panicked.
	dir := t.TempDir()

	t.Run("--cors=false turns off CORS set in code", func(t *testing.T) {
		c, err := loadConfig(t, Setup().CORS(), ConfigArgs([]string{"--cors=false"}))
		require.NoError(t, err)
		require.Nil(t, c.CORS)
	})

	t.Run("false in a file turns sections off", func(t *testing.T) {
		path := writeConfigFile(t, dir, "off.yaml", "proxy: false\nrecord: false\n")

		c, err := loadConfig(t, Setup().Proxy().Record(), ConfigFile(path))
		require.NoError(t, err)
		require.Nil(t, c.Proxy)
		require.Nil(t, c.Record)
	})

	t.Run("VLATTE_PROXY=true turns the proxy on with defaults", func(t *testing.T) {
		t.Setenv("VLATTE_PROXY", "true")

		c, err := loadConfig(t, Setup(), ConfigEnv())
		require.NoError(t, err)
		require.Equal(t, &_defaultProxyConfig, c.Proxy)
	})

	t.Run("a section key turns the section on and keeps code settings", func(t *testing.T) {
		c, err := loadConfig(t, Setup().CORS(CORS().AllowOrigin("https://code.org")), ConfigArgs([]string{"--cors-max-age", "60"}))
		require.NoError(t, err)
		require.Equal(t, "https://code.org", c.CORS.AllowedOrigin)
		require.Equal(t, 60, c.CORS.MaxAge)
	})

	t.Run("turning a section off and setting its keys in one source conflict", func(t *testing.T) {
		_, err := loadConfig(t, Setup(), ConfigArgs([]string{"--cors=false", "--cors-max-age", "60"}))
		require.Equal(t, config.CodeConflict, configProblems(t, err)[0].Code)
	})
}

func TestConfigSources_ProxyTimeoutUnits(t *testing.T) {
	// B4: numbers were read as nanoseconds, while the help said milliseconds.
	path := writeConfigFile(t, t.TempDir(), "proxy.json", `{"proxy": {"timeout": 5000}}`)
	c, err := loadConfig(t, Setup(), ConfigFile(path))
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, c.Proxy.Timeout)

	t.Setenv("VLATTE_PROXY_TIMEOUT", "250")
	c, err = loadConfig(t, Setup(), ConfigEnv())
	require.NoError(t, err)
	require.Equal(t, 250*time.Millisecond, c.Proxy.Timeout)

	c, err = loadConfig(t, Setup(), ConfigArgs([]string{"--proxy-timeout", "1.5s"}))
	require.NoError(t, err)
	require.Equal(t, 1500*time.Millisecond, c.Proxy.Timeout)
}

func TestConfigSources_ConfigFlagAndEnvPickTheFile(t *testing.T) {
	// B10: an explicit --config path used to be ignored.
	dir := t.TempDir()
	fromEnv := writeConfigFile(t, dir, "env.yaml", "name: env-file\n")
	fromFlag := writeConfigFile(t, dir, "flag.yaml", "name: flag-file\n")
	lookup := ConfigFile(DefaultConfigFileName, t.TempDir())

	t.Setenv("VLATTE_CONFIG", fromEnv)

	c, err := loadConfig(t, Setup(), lookup, ConfigEnv())
	require.NoError(t, err)
	require.Equal(t, "env-file", c.Name)

	c, err = loadConfig(t, Setup(), lookup, ConfigEnv(), ConfigArgs([]string{"--config", fromFlag}))
	require.NoError(t, err)
	require.Equal(t, "flag-file", c.Name, "flags outrank env")

	missing := filepath.Join(dir, "missing.yaml")
	_, err = loadConfig(t, Setup(), lookup, ConfigArgs([]string{"--config", missing}))
	require.Equal(t, []config.Problem{{
		Code:    config.CodeFileNotFound,
		Origin:  "flag --config",
		Message: fmt.Sprintf("config file %q does not exist", missing),
	}}, configProblems(t, err))
}

func TestConfigSources_OnlyJSONAndYAMLFiles(t *testing.T) {
	// B11: formats viper couldn't decode were advertised and silently ignored. A file named outright must be JSON
	// or YAML; a lookup counts only those, so an editor backup next to .vlatterc.json can't make it ambiguous.
	_, err := loadConfig(t, Setup(), ConfigFile("testdata/config_locals/diff_ext/.03_vlatterc.test.properties"))
	require.Equal(t, config.CodeUnsupportedFormat, configProblems(t, err)[0].Code)

	dir := t.TempDir()
	writeConfigFile(t, dir, ".vlatterc.toml", "name = 'toml'\n")
	writeConfigFile(t, dir, ".vlatterc.json~", `{"name": "backup"}`)

	c, err := loadConfig(t, Setup(), ConfigFile(DefaultConfigFileName, dir))
	require.NoError(t, err)
	require.Empty(t, c.Name, "neither file counts")

	writeConfigFile(t, dir, ".vlatterc.json", `{"name": "api"}`)

	c, err = loadConfig(t, Setup(), ConfigFile(DefaultConfigFileName, dir))
	require.NoError(t, err)
	require.Equal(t, "api", c.Name)
}

func TestConfigSources_Precedence(t *testing.T) {
	// Code is the base; then the file, env and args, each overriding the one before.
	dir := t.TempDir()
	writeConfigFile(t, dir, ".vlatterc.yaml", "name: file\n")
	t.Setenv("VLATTE_NAME", "env")

	file := ConfigFile(DefaultConfigFileName, dir)
	args := ConfigArgs([]string{"--name", "args"})

	tests := []struct {
		sources []ConfigSource
		want    string
	}{
		{[]ConfigSource{file, ConfigEnv(), args}, "args"},
		{[]ConfigSource{file, ConfigEnv()}, "env"},
		{[]ConfigSource{file}, "file"},
		{nil, "code"},
	}

	for _, tc := range tests {
		c, err := loadConfig(t, Setup().Name("code"), tc.sources...)
		require.NoError(t, err)
		require.Equal(t, tc.want, c.Name)
	}
}

func TestConfigSources_Defaults(t *testing.T) {
	t.Run("nil reads the file and env", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, ".vlatterc.json", `{"addr": ":0", "name": "file"}`)
		t.Chdir(dir)
		t.Setenv("VLATTE_NAME", "env")

		m, err := New(&Config{})
		require.NoError(t, err)
		require.Equal(t, "env", m.Config().Name)
		require.Equal(t, ":0", m.Config().Addr)
	})

	t.Run("empty reads nothing", func(t *testing.T) {
		t.Setenv("VLATTE_NAME", "env")
		t.Setenv("VLATTE_NOT_A_SETTING", "x")

		c, err := loadConfig(t, Setup().Name("code"))
		require.NoError(t, err)
		require.Equal(t, "code", c.Name)
	})

	t.Run("VLATTE_DOCKER_HOST is not a server setting but is allowed", func(t *testing.T) {
		t.Setenv("VLATTE_DOCKER_HOST", "0.0.0.0")

		_, err := New()
		require.NoError(t, err)
	})
}

func TestConfigSources_FileFormatsAgree(t *testing.T) {
	// The same settings in JSON, YAML and YML must give the same server.
	want := &Config{
		Name: "test_api",
		Addr: ":8080",
		CORS: &CORSConfig{
			AllowedOrigin:     "https://example.org",
			AllowCredentials:  true,
			AllowedMethods:    "GET,POST",
			AllowedHeaders:    "*",
			ExposeHeaders:     "None",
			MaxAge:            150,
			SuccessStatusCode: 200,
		},
		Proxy: &ProxyConfig{Via: "https://proxy.org/test", Timeout: 5 * time.Second},
		Record: &RecordConfig{
			Dir:                    "nowhere",
			Format:                 "json",
			RequestHeaders:         []string{"header1", "header2"},
			ExcludeResponseHeaders: []string{"header3", "header4"},
			BodyFiles:              true,
			MaxInlineBody:          _defaultMaxInlineBody,
			MaxBodySize:            _defaultMaxBodySize,
			Repeats:                vlatte.RecordSequence,
		},
	}

	for _, name := range []string{".01_vlatterc.test.json", ".02_vlatterc.test.yaml", ".02_vlatterc.test.yml"} {
		c, err := loadConfig(t, Setup(), ConfigFile(filepath.Join("testdata/config_locals/diff_ext", name)))

		require.NoError(t, err, name)
		require.Equal(t, want.Name, c.Name, name)
		require.Equal(t, want.Addr, c.Addr, name)
		require.Equal(t, want.CORS, c.CORS, name)
		require.Equal(t, want.Proxy, c.Proxy, name)
		require.Equal(t, want.Record, c.Record, name)
	}
}

func TestConfigSources_Hazards(t *testing.T) {
	t.Run("another server's proxy_all is never modified", func(t *testing.T) {
		// A server made from another's Config loads its sources onto a copy of its proxy_all: the other server keeps
		// its own settings.
		first, err := New(Setup().Silent().ConfigSources(ConfigArgs([]string{
			"--proxy-all-target", "http://first.local", "--proxy-all-remove-request-headers", "X-A",
		})))
		require.NoError(t, err)

		conf := *first.Config()
		conf.ConfigSources = []ConfigSource{ConfigArgs([]string{"--proxy-all-target", "http://second.local"})}

		second, err := New(&conf)
		require.NoError(t, err)

		require.Equal(t, "http://first.local", first.Config().proxyAll.target)
		require.Equal(t, "http://second.local", second.Config().proxyAll.target)
		require.Equal(t, []string{"X-A"}, second.Config().proxyAll.removeRequestHeaders, "the rest stays as the other's")
	})

	t.Run("a shared CORS struct is never modified", func(t *testing.T) {
		before := DefaultCORSConfig
		t.Setenv("VLATTE_CORS_MAX_AGE", "60")

		m, err := New(&Config{CORS: &DefaultCORSConfig, ConfigSources: []ConfigSource{ConfigEnv()}})
		require.NoError(t, err)
		require.Equal(t, 60, m.Config().CORS.MaxAge)
		require.Equal(t, before, DefaultCORSConfig)
	})

	t.Run("bad values are problems, not panics", func(t *testing.T) {
		_, err := loadConfig(t, Setup(), ConfigArgs([]string{
			"--proxy-via", "::not a url", "--record-format", "exe", "--record-target", "https://api.example.com/v1",
		}))

		problems := configProblems(t, err)
		require.Len(t, problems, 3)
		require.Equal(t, "proxy.via", problems[0].Key)
		require.Equal(t, "record.format", problems[1].Key)
		require.Equal(t, "record.target", problems[2].Key)
		require.Contains(t, problems[2].Message, "expected no path, query or fragment")
	})

	t.Run("a proxy password never reaches the error, even in an invalid URL", func(t *testing.T) {
		t.Setenv("VLATTE_PROXY_VIA", "http://user:secret@proxy:port")

		// --proxy is a bool, so the URL after it is an unexpected argument.
		_, err := loadConfig(t, Setup(), ConfigEnv(), ConfigArgs([]string{"--proxy", "http://user:secret@proxy:3128"}))

		require.Len(t, configProblems(t, err), 2)
		require.NotContains(t, err.Error(), "secret")
		require.Contains(t, err.Error(), `proxy.via="http://user:xxxxx@proxy:port"`)
		require.Contains(t, err.Error(), `unexpected argument "http://user:xxxxx@proxy:3128"`)
	})
}

func TestConfigSources_TLSFiles(t *testing.T) {
	// Each TLS problem names the setting to fix. A CA file without certificates must fail here:
	// the server would start, then reject every client.
	wd, err := os.Getwd()
	require.NoError(t, err)

	hint := "relative paths start at " + wd
	absent := filepath.Join(t.TempDir(), "absent.pem") // an absolute path needs no hint

	tests := []struct {
		name string
		args []string
		want []config.Problem
	}{
		{
			name: "a certificate needs its key",
			args: []string{"--tls-cert", _testCertFile},
			want: []config.Problem{{
				Code: config.CodeMissingValue, Origin: "flag --tls-cert", Key: "tls.key",
				Message: "tls.cert is set but tls.key is not", Hint: "set both",
			}},
		},
		{
			name: "a key needs its certificate",
			args: []string{"--tls-key", _testKeyFile},
			want: []config.Problem{{
				Code: config.CodeMissingValue, Origin: "flag --tls-key", Key: "tls.cert",
				Message: "tls.key is set but tls.cert is not", Hint: "set both",
			}},
		},
		{
			name: "client CAs need the server's certificate and key",
			args: []string{"--tls-ca", _testClientCertFile},
			want: []config.Problem{{
				Code: config.CodeMissingValue, Origin: "flag --tls-ca", Key: "tls.cert",
				Message: "tls.ca is set but tls.cert and tls.key are not", Hint: "mutual TLS needs the server's certificate and key too",
			}},
		},
		{
			name: "client CAs without certificates",
			args: []string{"--tls-cert", _testCertFile, "--tls-key", _testKeyFile, "--tls-ca", _testKeyFile},
			want: []config.Problem{{
				Code: config.CodeInvalidValue, Origin: "flag --tls-ca", Key: "tls.ca", Value: _testKeyFile,
				Message: "can't load the client CAs: " + _testKeyFile + " has no PEM certificates", Hint: hint,
			}},
		},
		{
			name: "every file that can't be loaded",
			args: []string{"--tls-cert", "missing.pem", "--tls-key", "missing.pem", "--tls-ca", absent},
			want: []config.Problem{
				{
					Code: config.CodeInvalidValue, Origin: "flag --tls-cert", Key: "tls.cert", Value: "missing.pem",
					Message: "can't load the certificate and key: open missing.pem: no such file or directory", Hint: hint,
				},
				{
					Code: config.CodeInvalidValue, Origin: "flag --tls-ca", Key: "tls.ca", Value: absent,
					Message: "can't load the client CAs: open " + absent + ": no such file or directory",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig(t, Setup(), ConfigArgs(tc.args))
			require.Equal(t, tc.want, configProblems(t, err))
		})
	}
}

func TestConfigSources_ValueChecks(t *testing.T) {
	// Sources accept every value the server can use, and reject the rest with what they expect.
	viaFile := writeConfigFile(t, t.TempDir(), "via.yaml", "proxy:\n  via: http://proxy:3128\n")

	tests := []struct {
		name    string
		setup   *ConfigBuilder
		sources []ConfigSource
		check   func(t *testing.T, c *Config)
		problem string // message of the only problem
	}{
		{
			name:    "a bare port, which the server listens on as :port",
			sources: []ConfigSource{ConfigArgs([]string{"--addr", "8080"})},
			check:   func(t *testing.T, c *Config) { require.Equal(t, "8080", c.Addr) },
		},
		{
			name:    "an empty address, which picks a random port, over code",
			setup:   Setup().Addr(":8080"),
			sources: []ConfigSource{ConfigArgs([]string{"--addr="})},
			check:   func(t *testing.T, c *Config) { require.Empty(t, c.Addr) },
		},
		{
			name:    "an address without a port",
			sources: []ConfigSource{ConfigArgs([]string{"--addr", "localhost"})},
			problem: `addr="localhost": expected host:port, :port or a port number`,
		},
		{
			name:    "a negative body size",
			sources: []ConfigSource{ConfigArgs([]string{"--log-max-body-size=-1"})},
			problem: `log.max_body_size="-1": expected 0 or more`,
		},
		{
			name:    "a max age below -1",
			sources: []ConfigSource{ConfigArgs([]string{"--cors-max-age=-2"})},
			problem: `cors.max_age="-2": expected -1 or more`,
		},
		{
			name:    "an empty record target, which clears one from code",
			setup:   Setup().Record(RecordTarget("https://api.example.com")),
			sources: []ConfigSource{ConfigArgs([]string{"--record-target="})},
			check: func(t *testing.T, c *Config) {
				require.NotNil(t, c.Record)
				require.Empty(t, c.Record.Target)
			},
		},
		{
			name:    "an empty proxy via, which clears one from a lower source",
			sources: []ConfigSource{ConfigFile(viaFile), ConfigArgs([]string{"--proxy-via="})},
			check: func(t *testing.T, c *Config) {
				require.NotNil(t, c.Proxy)
				require.Empty(t, c.Proxy.Via)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setup := tc.setup
			if setup == nil {
				setup = Setup()
			}

			c, err := loadConfig(t, setup, tc.sources...)
			if tc.problem != "" {
				problems := configProblems(t, err)
				require.Len(t, problems, 1)
				require.Equal(t, tc.problem, problems[0].Message)

				return
			}

			require.NoError(t, err)
			tc.check(t, c)
		})
	}
}

func TestConfigSources_ErrorListsEveryProblemWithItsOrigin(t *testing.T) {
	// One run must show every problem of every source, each with where it came from and how to fix it.
	path := writeConfigFile(t, t.TempDir(), "settings.json", `{"cors": {"allowed_origins": "*"}, "log": {"level": "verbose"}}`)
	t.Setenv("VLATTE_LOGLEVEL", "1")
	t.Setenv("VLATTE_PROXY_TIMEOUT", "10x")

	_, err := loadConfig(t, Setup(), ConfigFile(path), ConfigEnv(), ConfigArgs([]string{"--tls-cert", _testCertFile}))

	want := `server: error loading configuration sources
config: 5 problems
  env VLATTE_LOGLEVEL: unknown_key: VLATTE_LOGLEVEL is not a known setting; did you mean VLATTE_LOG_LEVEL?
  env VLATTE_PROXY_TIMEOUT: invalid_value: proxy.timeout must be milliseconds or a Go duration, not "10x"; such as 1500 or 1.5s
  file ` + path + `: unknown_key: cors.allowed_origins is not a known setting; did you mean cors.allowed_origin?
  file ` + path + `: invalid_value: log.level must be one of trace, debug, info, warn, error, fatal, panic, none or disabled, not "verbose"
  flag --tls-cert: missing_value: tls.cert is set but tls.key is not; set both`

	require.EqualError(t, err, want)
}

func TestConfigUsage_ListsEverySetting(t *testing.T) {
	usage := ConfigUsage()

	for _, f := range ConfigFlags() {
		key := strings.ReplaceAll(f.Name, "-", "_")
		require.Contains(t, usage, "--"+f.Name)
		require.Contains(t, usage, "env "+EnvPrefix+"_"+strings.ToUpper(key))
	}

	require.Contains(t, usage, "file key log.max_body_size")
	require.Contains(t, usage, "Default: info.")
	require.Equal(t, "42", choiceName(_logLevels, 42), "a default without a name shows its number")
}

// writeTestCA writes the certificate and the key of a new CA, as PEM files in dir, and returns their paths.
func writeTestCA(t *testing.T, dir string) (string, string) {
	t.Helper()

	ca, err := newCA()
	require.NoError(t, err)

	key, err := x509.MarshalPKCS8PrivateKey(ca.PrivateKey)
	require.NoError(t, err)

	certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate[0]}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600))

	return certFile, keyFile
}

func TestConfigSources_InterceptionNeedsACA(t *testing.T) {
	// A CA the server makes lives as long as the server, and a client of the vlatte command can't get it to trust: the
	// settings that intercept need the CA's files, both of them.
	dir := t.TempDir()
	caCert, _ := writeTestCA(t, dir)

	tests := map[string]struct {
		args []string
		want string
	}{
		"no CA":      {[]string{"--proxy-intercept"}, "proxy.intercept is set but proxy.ca_cert and proxy.ca_key are not"},
		"no CA key":  {[]string{"--proxy-intercept", "--proxy-ca-cert", caCert}, "proxy.ca_cert is set but proxy.ca_key is not"},
		"no CA cert": {[]string{"--proxy-intercept", "--proxy-ca-key", caCert}, "proxy.ca_key is set but proxy.ca_cert is not"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(t, Setup(), ConfigFile(DefaultConfigFileName, t.TempDir()), ConfigArgs(tt.args))
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestConfigSources_TheCAOfAProxyTurnedOffLeavesItOff(t *testing.T) {
	// Loading the CA's files turned the forward proxy on again, after a later source turned it off.
	dir := t.TempDir()
	caCert, caKey := writeTestCA(t, dir)
	file := writeConfigFile(t, dir, "settings.yaml",
		"proxy:\n  intercept: true\n  ca_cert: "+caCert+"\n  ca_key: "+caKey+"\n")

	c, err := loadConfig(t, Setup(), ConfigFile(file), ConfigArgs([]string{"--proxy=false"}))
	require.NoError(t, err)
	require.Nil(t, c.Proxy)
}
