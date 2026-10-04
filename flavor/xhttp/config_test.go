package xhttp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"text/template"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp/cors"
	"github.com/caffeine-projects/vlatte/flavor/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/vlattecore/kv/kvmem"
)

type testBodyParser struct{}

func (p testBodyParser) CanParse(content string, r *http.Request) bool {
	return content == httpval.MIMETextPlain && r.Header.Get("x-test") == "num"
}

func (p testBodyParser) Parse(body []byte, _ *http.Request) (any, error) {
	return strconv.Atoi(string(body))
}

type customTestServerFactory struct {
	decorated vlatte.ServerFactory[ServerFactoryConfig]
}

func (f *customTestServerFactory) Server(config ServerFactoryConfig) (vlatte.Server, error) {
	srv, err := f.decorated.Server(config)
	if err != nil {
		return nil, err
	}
	return &customTestServer{decorated: srv}, nil
}

type customTestServer struct {
	decorated vlatte.Server
}

func (s *customTestServer) Start() error {
	return s.decorated.Start()
}

func (s *customTestServer) StartTLS() error {
	return s.decorated.StartTLS()
}

func (s *customTestServer) Close() error {
	return s.decorated.Close()
}

func (s *customTestServer) Info() *vlatte.ServerInfo {
	return s.decorated.Info()
}

func TestConfig(t *testing.T) {
	client := &http.Client{}

	t.Run("should run server with the custom given address", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())

		m := New(Setup().Addr(addr))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(
			Getf("/test").
				Reply(OK()))

		req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
		res, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.True(t, scoped.Called())
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Contains(t, m.server.Info().URL, addr)
	})

	t.Run("request body parsers from config should take precedence", func(t *testing.T) {
		m := New(Setup().RequestBodyParsers(&testBodyParser{}))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(Post(filter.URLPath("/test")).
			Body(filter.StrictEqual(10)).
			Reply(OK()))

		req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", strings.NewReader("10"))
		req.Header.Add(httpval.HeaderContentType, httpval.MIMETextPlain)
		req.Header.Add("x-test", "num")

		res, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.True(t, scoped.Called())
		require.Equal(t, http.StatusOK, res.StatusCode)
	})

	t.Run("middlewares from config should take precedence", func(t *testing.T) {
		msg := "hello world"
		middleware := func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					w.Header().Add("intercepted", "true")
					w.Header().Add(httpval.HeaderContentType, httpval.MIMETextPlain)
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(msg))
				})
		}

		m := New(Setup().Middlewares(middleware))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(
			Get(filter.URLPath("/test")).
				Reply(OK()))

		req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
		res, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.False(t, scoped.Called())
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, "true", res.Header.Get("intercepted"))
	})

	t.Run("configure custom server", func(t *testing.T) {
		m := New(Setup().ServerFactory(&customTestServerFactory{decorated: newServerFactory()}))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(
			Get(filter.URLPath("/test")).
				Reply(OK()))

		req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
		res, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.True(t, scoped.Called())
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestConfigBuilder(t *testing.T) {
	addr := ""
	nm := "test"
	customLogger := zerolog.Nop()
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	m := New(Setup().
		Name(nm).
		Addr(addr).
		RootDir("test_root_dir").
		MockNotFoundStatusCode(http.StatusNotFound).
		RequestBodyParsers(&plainTextParser{}).
		MaxBodyParsingLimit(10).
		NoBodyParsing().
		Middlewares().
		CORS(&cors.DefaultConfig).
		ServerFactory(&httpTestServerFactory{}).
		HandlerDecorator(func(handler http.Handler) http.Handler { return handler }).
		LogVerbosity(vlatte.LogBasic).
		LogLevel(vlatte.LogLevelInfo).
		LogPretty(false).
		LogBodyMaxSize(100).
		Logger(&customLogger).
		UseDescriptiveLogger().
		RedactHeader("header-1", "HEADER-2", "hEaDeR-3").
		Parameters(kvmem.New()).
		MockFilePatterns("test", "dev").
		Loader(&fileLoader{}).
		MockFileHandlers(&customMockFileHandler{}).
		TemplateEngineFunctions(template.FuncMap{"trim": strings.TrimSpace}).
		Proxy(&ProxyConfig{}, &ProxyConfig{}).
		TLSConfig(tlsConfig).
		TLSCertKeyPair("test/testdata/cert/cert.pem", "test/testdata/cert/key.pem").
		TLSMutual("test/testdata/cert/cert.pem", "test/testdata/cert/key.pem", "test/testdata/cert/cert_client.pem"))
	conf := m.Config()

	require.Equal(t, nm, conf.Name)
	require.Equal(t, addr, conf.Addr)
	require.Equal(t, "test_root_dir", conf.RootDir)
	require.Equal(t, http.StatusNotFound, conf.RequestWasNotMatchedStatusCode)
	require.Len(t, conf.RequestBodyParsers, 1)
	require.EqualValues(t, conf.MaxBodyParsingLimit, 10)
	require.True(t, conf.NoBodyParsing)
	require.Len(t, conf.Middlewares, 0)
	require.Equal(t, &cors.DefaultConfig, conf.CORS)
	require.NotNil(t, conf.HandlerDecorator)
	require.Equal(t, vlatte.LogBasic, conf.LogVerbosity)
	require.Equal(t, vlatte.LogLevelInfo, conf.LogLevel)
	require.False(t, conf.LogPretty)
	require.Equal(t, &customLogger, conf.Logger)
	require.Equal(t, int64(100), conf.LogBodyMaxSize)
	require.Equal(t, kvmem.New(), conf.Parameters)
	require.Equal(t, []string{"test", "dev"}, conf.MockFileSearchPatterns)
	require.True(t, conf.Debug)
	require.Len(t, conf.HeaderNamesToRedact, 3)
	require.Equal(t, conf.HeaderNamesToRedact["header-1"], struct{}{})
	require.Equal(t, conf.HeaderNamesToRedact["header-2"], struct{}{})
	require.Equal(t, conf.HeaderNamesToRedact["header-3"], struct{}{})
	require.Len(t, conf.Loaders, 1)
	require.Len(t, conf.MockFileHandlers, 1)
	require.Len(t, conf.TemplateFunctions, 1)
	require.NotNil(t, conf.Proxy)
	require.Equal(t, tlsConfig, conf.TLSConfig)
	require.NotNil(t, conf.TLSCertificates)
	require.NotNil(t, conf.TLSClientCAs)
}

func TestLogLevelString(t *testing.T) {
	require.Equal(t, vlatte.LogBasic.String(), "basic")
	require.Equal(t, vlatte.LogHeader.String(), "header")
	require.Equal(t, vlatte.LogBody.String(), "body")
}

func TestNew_ZeroValueConfig(t *testing.T) {
	// A Config built by hand leaves unset fields at zero values; the server must still answer unmatched requests.
	m := New(&Config{})
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := http.Get(m.URL("/nothing"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestGlobalEventsAreApplied(t *testing.T) {
	var fired atomic.Int32
	setup := Setup().OnCompleteListener("count", vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
		fired.Add(1)
		return nil
	}))
	setup.Base().GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "count"}}})

	m := New(setup)
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/event").Reply(OK()))

	res, err := http.Get(m.URL("/event"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	// Global on_complete events run for every matched request, after the response is written.
	require.Eventually(t, func() bool { return fired.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}
