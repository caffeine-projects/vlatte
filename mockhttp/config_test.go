package mockhttp

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

type testBodyParser struct{}

func (p testBodyParser) CanParse(content string, r *http.Request) bool {
	return content == MIMETextPlain && r.Header.Get("x-test") == "num"
}

func (p testBodyParser) Parse(body []byte, _ *http.Request) (any, error) {
	return strconv.Atoi(string(body))
}

type customTestBackendFactory struct {
	decorated BackendFactory
}

func (f *customTestBackendFactory) NewBackend(config BackendConfig) (Backend, error) {
	srv, err := f.decorated.NewBackend(config)
	if err != nil {
		return nil, err
	}
	return &customTestBackend{decorated: srv}, nil
}

type customTestBackend struct {
	decorated Backend
}

func (s *customTestBackend) Start() error {
	return s.decorated.Start()
}

func (s *customTestBackend) StartTLS() error {
	return s.decorated.StartTLS()
}

func (s *customTestBackend) Close() error {
	return s.decorated.Close()
}

func (s *customTestBackend) Info() *BackendInfo {
	return s.decorated.Info()
}

func TestConfig(t *testing.T) {
	client := &http.Client{}

	t.Run("should run server with the custom given address", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := l.Addr().String()
		require.NoError(t, l.Close())

		m := MustNew(Setup().Addr(addr))
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
		m := MustNew(Setup().RequestBodyParsers(&testBodyParser{}))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(Post(match.URLPath("/test")).
			Body(match.StrictEqual(10)).
			Reply(OK()))

		req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", strings.NewReader("10"))
		req.Header.Add(HeaderContentType, MIMETextPlain)
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
					w.Header().Add(HeaderContentType, MIMETextPlain)
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(msg))
				})
		}

		m := MustNew(Setup().Use(middleware))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(
			Get(match.URLPath("/test")).
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
		m := MustNew(Setup().BackendFactory(&customTestBackendFactory{decorated: newBackendFactory()}))
		t.Cleanup(func() { m.Close() })
		m.MustStart()

		scoped := m.MustMock(
			Get(match.URLPath("/test")).
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
	engine := vlatte.MustNewGoTemplateEngine()

	m := MustNew(Setup().
		Name(nm).
		Addr(addr).
		RootDir("test_root_dir").
		MockNotFoundStatusCode(http.StatusNotFound).
		ErrorStatusCode(http.StatusInternalServerError).
		RequestBodyParsers(&plainTextParser{}).
		MaxBodyParsingLimit(10).
		NoBodyParsing().
		NoDebugHeaders().
		Use().
		CORS(&DefaultCORSConfig).
		BackendFactory(&httptestBackendFactory{}).
		HandlerDecorator(func(handler http.Handler) http.Handler { return handler }).
		LogVerbosity(vlatte.LogBasic).
		LogLevel(vlatte.LogLevelInfo).
		LogPretty(false).
		LogBodyMaxSize(100).
		Logger(&customLogger).
		UseDescriptiveLogger().
		RedactHeader("header-1", "HEADER-2", "hEaDeR-3").
		Parameters(vlatte.NewMemoryKV()).
		MockFilePatterns("test", "dev").
		Loader(&fileLoader{}).
		MockFileHandlers(&customMockFileHandler{}).
		TemplateEngine(engine).
		Proxy(&ProxyConfig{}, &ProxyConfig{}).
		TLSConfig(tlsConfig).
		TLSCertKeyPair("e2e/testdata/cert/cert.pem", "e2e/testdata/cert/key.pem").
		TLSMutual("e2e/testdata/cert/cert.pem", "e2e/testdata/cert/key.pem", "e2e/testdata/cert/cert_client.pem"))
	conf := m.Config()

	require.Equal(t, nm, conf.Name)
	require.Equal(t, addr, conf.Addr)
	require.Equal(t, "test_root_dir", conf.RootDir)
	require.Equal(t, http.StatusNotFound, conf.RequestWasNotMatchedStatusCode)
	require.Equal(t, http.StatusInternalServerError, conf.ErrorStatusCode)
	require.Len(t, conf.RequestBodyParsers, 1)
	require.EqualValues(t, conf.MaxBodyParsingLimit, 10)
	require.True(t, conf.NoBodyParsing)
	require.True(t, conf.NoDebugHeaders)
	require.Len(t, conf.Middlewares, 0)
	require.Equal(t, &DefaultCORSConfig, conf.CORS)
	require.NotNil(t, conf.HandlerDecorator)
	require.Equal(t, vlatte.LogBasic, conf.LogVerbosity)
	require.Equal(t, vlatte.LogLevelInfo, conf.LogLevel)
	require.False(t, conf.LogPretty)
	require.Equal(t, &customLogger, conf.Logger)
	require.Equal(t, int64(100), conf.LogBodyMaxSize)
	require.Equal(t, vlatte.NewMemoryKV(), conf.Parameters)
	require.Equal(t, []string{"test", "dev"}, conf.MockFileSearchPatterns)
	require.True(t, conf.Debug)
	require.Len(t, conf.HeaderNamesToRedact, 3)
	require.Equal(t, conf.HeaderNamesToRedact["header-1"], struct{}{})
	require.Equal(t, conf.HeaderNamesToRedact["header-2"], struct{}{})
	require.Equal(t, conf.HeaderNamesToRedact["header-3"], struct{}{})
	require.Len(t, conf.Loaders, 1)
	require.Len(t, conf.MockFileHandlers, 1)
	require.Same(t, engine, conf.TemplateEngine)
	require.NotNil(t, conf.Proxy)
	require.Equal(t, tlsConfig, conf.TLSConfig)
	require.NotNil(t, conf.TLSCertificates)
	require.NotNil(t, conf.TLSClientCAs)
}

func TestConfigBuilder_TLSMutualNeedsCACertificates(t *testing.T) {
	// An empty client CA pool would start a server that rejects every client.
	_, err := New(Setup().TLSMutual(_testCertFile, _testKeyFile, _testKeyFile))

	require.ErrorContains(t, err, _testKeyFile+" has no PEM certificates")
}

func TestLogLevelString(t *testing.T) {
	require.Equal(t, vlatte.LogBasic.String(), "basic")
	require.Equal(t, vlatte.LogHeader.String(), "header")
	require.Equal(t, vlatte.LogBody.String(), "body")
}

func TestNew_ZeroValueConfig(t *testing.T) {
	// A Config built by hand leaves unset fields at zero values; the server must still answer unmatched requests,
	// and failures, with a status a client can read.
	m := MustNew(&Config{})
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Reply(OK().BodyFile("missing.txt")))

	res, err := http.Get(m.URL("/nothing"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	res, err = http.Get(m.URL("/x"))
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
	setup.GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "count"}}})

	m := MustNew(setup)
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/event").Reply(OK()))

	res, err := http.Get(m.URL("/event"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	// Global on_complete events run for every matched request, and the response waits for them.
	require.Equal(t, int32(1), fired.Load())
}

func TestGlobalEventsMustNameARegisteredListener(t *testing.T) {
	// As a mock's events, a global event must name a registered listener: with a typo in the name, it would never
	// run, and nothing would say so.
	global := &vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "nobody"}}}
	somebody := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	for name, setup := range map[string]*ConfigBuilder{
		"other listeners":   Setup().OnCompleteListener("somebody", somebody),
		"no listener store": Setup().EventListeners(nil),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(setup.Silent().GlobalEvents(global))
			require.EqualError(t, err, "server: global on_complete listener nobody at index 0 is not registered")
		})
	}
}

func TestOnCompleteListener_ANameRegisteredTwiceFailsNew(t *testing.T) {
	// #18: the error of Register was dropped, so the second listener of a name never ran, without a word. Every name
	// registered twice fails New now, naming the listener, whether the registry is the builder's or a shared one.
	nop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	shared := vlatte.NewListener()
	require.NoError(t, shared.OnComplete.Register("audit", nop))

	_, err := New(Setup().Silent().
		OnCompleteListener("hook", nop).
		OnCompleteListener("hook", nop).
		OnCompleteListener("audit", nop).
		EventListeners(shared))

	require.ErrorIs(t, err, vlatte.ErrEventAlreadyRegistered)
	require.ErrorContains(t, err, "config: on_complete listener hook: vlatte: event already registered")
	require.ErrorContains(t, err, "config: on_complete listener audit: vlatte: event already registered")
}

func TestEventListeners_KeepsTheListenersRegisteredBefore(t *testing.T) {
	// EventListeners replaced the registry, so the listeners registered before it were dropped, without a word, and a
	// listener registered after EventListeners(nil) panicked. In any order, every listener is registered now.
	tests := []struct {
		name  string
		setup func(l vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder
	}{
		{
			name: "a shared registry set after",
			setup: func(l vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
				return Setup().OnCompleteListener("hook", l).EventListeners(vlatte.NewListener())
			},
		},
		{
			name: "a shared registry set before",
			setup: func(l vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
				return Setup().EventListeners(vlatte.NewListener()).OnCompleteListener("hook", l)
			},
		},
		{
			name: "no registry set after",
			setup: func(l vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
				return Setup().OnCompleteListener("hook", l).EventListeners(nil)
			},
		},
		{
			name: "no registry set before",
			setup: func(l vlatte.EventListener[vlatte.OnComplete]) *ConfigBuilder {
				return Setup().EventListeners(nil).OnCompleteListener("hook", l)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran := make(chan struct{}, 1)
			l := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
				ran <- struct{}{}
				return nil
			})

			m := MustNew(tc.setup(l).Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "hook"}).Reply(NoContent()))

			status, _ := get(t, m, "/x")
			require.Equal(t, http.StatusNoContent, status)

			select {
			case <-ran:
			case <-time.After(5 * time.Second):
				t.Fatal("the listener never ran")
			}
		})
	}
}

// acceptCounter counts the connections accepted through a listener.
type acceptCounter struct {
	net.Listener

	accepted atomic.Int32
}

func (l *acceptCounter) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return c, err
}

func TestListener(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := &acceptCounter{Listener: inner}

	m := MustNew(Setup().Listener(l))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/test").Reply(OK()))

	get := func() {
		t.Helper()
		res, err := http.Get(m.URL("/test"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
	}

	// The server serves the given listener itself, so its port is never free for another process to take.
	get()
	require.Equal(t, "http://"+inner.Addr().String(), m.URL())
	require.Positive(t, l.accepted.Load())

	// Close closes the listener.
	m.Close()

	_, err = inner.Accept()
	require.ErrorIs(t, err, net.ErrClosed)
}

func TestUse_MiddlewaresRunInTheOrderTheyAreAdded(t *testing.T) {
	// A middleware may depend on one before it, such as auth on a request ID: middlewares run in the order they are
	// added, and a later Use adds to the ones before it instead of replacing them.
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Order", name)
				next.ServeHTTP(w, r)
			})
		}
	}

	m := MustNew(Setup().Silent().Use(mark("a"), mark("b")).Use(mark("c")))
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Getf("/p").Reply(OK()))

	res, _ := do(t, http.MethodGet, m.URL("/p"), nil, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"a", "b", "c"}, res.Header.Values("X-Order"))
	vlatte.Times(t, scope, 1)
}

func TestUse_APanicInAMiddlewareIsRecovered(t *testing.T) {
	// A middleware that panics fails only its request, with the error status and VLatte-Error telling it was a
	// panic: the server goes on serving.
	var panicked atomic.Bool

	panicOnce := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if panicked.CompareAndSwap(false, true) {
				panic("middleware failed")
			}

			next.ServeHTTP(w, r)
		})
	}

	m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError).Use(panicOnce))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/p").Reply(OK()))

	res, _ := do(t, http.MethodGet, m.URL("/p"), nil, nil)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, vlatte.FailurePanic, res.Header.Get(HeaderVlatteError))

	res, _ = do(t, http.MethodGet, m.URL("/p"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestUse_MiddlewaresSeeEveryMethod(t *testing.T) {
	// Middlewares intercept every request the mocks get, extension methods included.
	m := MustNew(Setup().Silent().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Seen-Method", r.Method)
			next.ServeHTTP(w, r)
		})
	}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Request().Method("PURGE").URLPathf("/p").Reply(Accepted()))

	res, _ := do(t, "PURGE", m.URL("/p"), nil, nil)

	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, "PURGE", res.Header.Get("X-Seen-Method"))
}
