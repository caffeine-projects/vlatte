package mockhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
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

func (s *customTestBackend) Listen() error {
	return s.decorated.Listen()
}

func (s *customTestBackend) ListenTLS() error {
	return s.decorated.ListenTLS()
}

func (s *customTestBackend) Serve() error {
	return s.decorated.Serve()
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

func TestConfig_ApplyCopiesEveryField(t *testing.T) {
	// Apply copies each field by hand: a field added to Config without its line in Apply would be dropped by every
	// Setup() and Config given to New, without a word. Every exported field is set, and each must arrive.
	samples := map[reflect.Type]any{
		reflect.TypeFor[net.Listener]():          &net.TCPListener{},
		reflect.TypeFor[BackendFactory]():        newBackendFactory(),
		reflect.TypeFor[vlatte.TemplateEngine](): vlatte.MustNewGoTemplateEngine(),
		reflect.TypeFor[http.RoundTripper]():     &http.Transport{},
		reflect.TypeFor[vlatte.KV]():             vlatte.NewMemoryKV(),
	}

	src := &Config{}
	sv := reflect.ValueOf(src).Elem()

	for i := range sv.NumField() {
		field := sv.Type().Field(i)
		if !field.IsExported() {
			continue
		}

		value := sv.Field(i)
		switch kind := field.Type.Kind(); kind {
		case reflect.String:
			value.SetString("x")
		case reflect.Bool:
			value.SetBool(true)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value.SetInt(1)
		case reflect.Pointer:
			value.Set(reflect.New(field.Type.Elem()))
		case reflect.Slice:
			value.Set(reflect.MakeSlice(field.Type, 1, 1))
		case reflect.Map:
			value.Set(reflect.MakeMap(field.Type))
		case reflect.Func:
			value.Set(reflect.MakeFunc(field.Type, func([]reflect.Value) []reflect.Value { return nil }))
		case reflect.Interface:
			sample, ok := samples[field.Type]
			require.True(t, ok, "Config.%s: add a sample of %s to this test", field.Name, field.Type)
			value.Set(reflect.ValueOf(sample))
		default:
			t.Fatalf("Config.%s: teach this test to set a %s", field.Name, kind)
		}
	}

	dst := &Config{}
	require.NoError(t, src.Apply(dst))

	dv := reflect.ValueOf(dst).Elem()
	for i := range dv.NumField() {
		if field := dv.Type().Field(i); field.IsExported() {
			require.False(t, dv.Field(i).IsZero(), "Config.Apply doesn't copy %s", field.Name)
		}
	}
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

func TestGlobalEventsCanCarryTheirListener(t *testing.T) {
	// A global event set in Go can carry its listener, as a callback does: it needs none registered, nor a listener
	// store, and runs for every matched request.
	for name, setup := range map[string]*ConfigBuilder{
		"listeners":         Setup(),
		"no listener store": Setup().EventListeners(nil),
	} {
		t.Run(name, func(t *testing.T) {
			var fired atomic.Int32
			counts := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
				fired.Add(1)
				return nil
			})

			m := MustNew(setup.Silent().GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "counts", Listener: counts}}}))
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(Getf("/a").Reply(OK()), Getf("/b").Callback(counts).Reply(OK()))

			for _, path := range []string{"/a", "/b"} {
				status, _ := get(t, m, path)
				require.Equal(t, http.StatusOK, status)
			}

			require.Equal(t, int32(3), fired.Load(), "the global event ran twice, and the callback of /b once")
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

	// Without a shared registry, each server's own registry fails the same.
	_, err = New(Setup().Silent().OnCompleteListener("hook", nop).OnCompleteListener("hook", nop))
	require.ErrorIs(t, err, vlatte.ErrEventAlreadyRegistered)
	require.ErrorContains(t, err, "config: on_complete listener hook: vlatte: event already registered")
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

func TestOnCompleteListener_ReachesOnlyTheServersBuiltAfter(t *testing.T) {
	// OnCompleteListener registered as it was called, in a registry every server built from the builder shared, so a
	// listener registered after a server was built reached it too. Each server now has the listeners registered when
	// it was built, and a builder builds any number of servers.
	nop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	setup := Setup().Silent().OnCompleteListener("first", nop)

	before := MustNew(setup)
	t.Cleanup(before.Close)

	setup.OnCompleteListener("second", nop)

	after := MustNew(setup)
	t.Cleanup(after.Close)

	require.True(t, before.Config().Listeners.OnComplete.Has("first"))
	require.False(t, before.Config().Listeners.OnComplete.Has("second"), "a listener registered later reached a server built before")
	require.True(t, after.Config().Listeners.OnComplete.Has("first"))
	require.True(t, after.Config().Listeners.OnComplete.Has("second"))

	_, err := before.Mock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "second"}).Reply(NoContent()))
	require.ErrorContains(t, err, "mock: on_complete listener second at index 0 is not registered")
}

func TestEventListeners_ServersShareTheRegistry(t *testing.T) {
	// A registry given with EventListeners is shared on purpose: every server built from the builder has the
	// listeners registered in it, before or after.
	nop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	shared := vlatte.NewListener()
	setup := Setup().Silent().EventListeners(shared).OnCompleteListener("first", nop)

	before := MustNew(setup)
	t.Cleanup(before.Close)

	setup.OnCompleteListener("second", nop)

	require.Same(t, shared, before.Config().Listeners)
	require.True(t, before.Config().Listeners.OnComplete.Has("second"))
}

func TestEventListeners_NilGivesEachServerItsOwnRegistryAgain(t *testing.T) {
	// EventListeners(nil) after a shared registry goes back to a registry of each server's own: the listeners
	// registered so far reach the server, and those registered later don't reach the registry it stopped sharing.
	nop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	shared := vlatte.NewListener()
	setup := Setup().Silent().OnCompleteListener("hook", nop).EventListeners(shared).EventListeners(nil)

	m := MustNew(setup)
	t.Cleanup(m.Close)

	require.NotSame(t, shared, m.Config().Listeners)
	require.True(t, m.Config().Listeners.OnComplete.Has("hook"))

	setup.OnCompleteListener("later", nop)
	require.False(t, shared.OnComplete.Has("later"), "a listener registered after EventListeners(nil) reached the shared registry")
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

func TestConfig_TheServersFilesAreInItsWorkDir(t *testing.T) {
	// A server keeps its own files in its work directory: one of its name, when it has one, so servers of one
	// directory keep apart, unless workdir puts them elsewhere. Only a leading {workdir} stands for it.
	abs := filepath.Join(t.TempDir(), "work")

	tests := []struct {
		name string
		conf Config
		path string
		want string
	}{
		{name: "default", path: "{workdir}/_mocks", want: filepath.Join("testdata", "vlatte", "_mocks")},
		{name: "named", conf: Config{Name: "api"}, path: "{workdir}/_journal", want: filepath.Join("testdata", "vlatte", "api", "_journal")},
		{name: "set", conf: Config{Name: "api", WorkDir: "work"}, path: "{workdir}/_admin", want: filepath.Join("work", "_admin")},
		{name: "from the root", conf: Config{RootDir: "root"}, path: "{workdir}/_mocks", want: filepath.Join("root", "testdata", "vlatte", "_mocks")},
		{name: "absolute", conf: Config{RootDir: "root", WorkDir: abs + "/"}, path: "{workdir}/_mocks", want: filepath.Join(abs, "_mocks")},
		{name: "itself", path: "{workdir}", want: filepath.Join("testdata", "vlatte")},
		{name: "no placeholder", conf: Config{RootDir: "root"}, path: "mocks/*.yaml", want: filepath.Join("root", "mocks", "*.yaml")},
		{name: "another name", path: "{workdirs}/a", want: filepath.Join("{workdirs}", "a")},
		{name: "not leading", path: "a/{workdir}/b", want: filepath.Join("a", "{workdir}", "b")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.conf.path(tt.path))
		})
	}
}

func TestNew_ANameThatNamesTheWorkDirStaysInIt(t *testing.T) {
	// The name of a server is a folder of testdata/vlatte, next to vlatte's own, whose names start with _: a name
	// that would leave it, or take one of vlatte's, fails, unless workdir sets the folder. From settings, it is a
	// problem of the name, as a bad value of any setting is.
	for _, name := range []string{"..", ".", "../x", "/abs", "_mocks"} {
		_, err := New(Setup().Silent().Name(name))
		require.ErrorContains(t, err, "names the folder of the server's files", name)

		_, err = New(Setup().Silent().ConfigSources(ConfigArgs([]string{"--name", name})))
		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "%s: %v", name, err)
		require.Equal(t, "name", cfgErr.Problems[0].Key)
	}

	for _, setup := range []*ConfigBuilder{
		Setup().Name("api"),
		Setup().Name("orders/v1"),
		Setup().Name("..").WorkDir("work"),
		Setup().ConfigSources(ConfigArgs([]string{"--name", "..", "--workdir", "work"})),
	} {
		m, err := New(setup.Silent())
		require.NoError(t, err)
		m.Close()
	}
}

// longerThan builds the custom matcher [longer-than, n]: a body longer than n bytes.
func longerThan(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
	if len(args) != 1 {
		return nil, errors.New("takes a length")
	}

	n, ok := args[0].(int64)
	if !ok {
		return nil, errors.New("takes a length")
	}

	return match.Function(func(v any) (bool, error) {
		b, isBytes := v.([]byte)
		return isBytes && int64(len(b)) > n, nil
	}), nil
}

const _longerThanMock = `{"request": {"method": "POST", "body": ["longer-than", 3]}, "response": {"status": 200}}`

func TestConfigBuilder_Matchers_MockFilesNameCustomMatchers(t *testing.T) {
	m := MustNew(Setup().Silent().Matchers(matchbuild.MustNew(matchbuild.WithMatcher("longer-than", longerThan))))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte(_longerThanMock), "json"))

	res, _ := do(t, http.MethodPost, m.URL(), nil, []byte("abcd"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = do(t, http.MethodPost, m.URL(), nil, []byte("abc"))
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestConfigBuilder_Matchers_BelongToTheirServer(t *testing.T) {
	// A registry is the server's, not global: a server without it doesn't know its matchers.
	MustNew(Setup().Silent().Matchers(matchbuild.MustNew(matchbuild.WithMatcher("longer-than", longerThan)))).Close()

	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	_, err := m.Mock(FromBytes([]byte(_longerThanMock), "json"))
	require.ErrorContains(t, err, `unknown matcher "longer-than"`)
}
