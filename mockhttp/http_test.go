package mockhttp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/kv"
	"github.com/caffeine-projects/vlatte/match"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

func TestHTTPMockApp(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Header("test", match.StrictEqual("hello")).
			Query("filter", match.StrictEqual("all")).
			Reply(Created().
				PlainText("hello world")))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)
	req.Header.Add("test", "hello")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, 201, res.StatusCode)
	require.Equal(t, "hello world", string(body))
}

func TestResponseMapperModifyingResponse(t *testing.T) {
	const k = "key"
	const v = "test-ok"

	params := kv.NewMemory()
	err := params.Set(t.Context(), k, []byte(v))
	require.NoError(t, err)

	m := MustNew(Setup().Parameters(params))
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(Get(match.URLPath("/test")).
		Reply(OK()).
		Map(func(rv *RequestValues, r *Response) error {
			val, _ := rv.App.Parameters().Get(t.Context(), k)

			r.Header.Add("x-param-key", string(val))
			r.Header.Add("x-test", rv.RawRequest.Header.Get("x-param"))

			return nil
		}))

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/test", m.URL()), nil)
	req.Header.Add("x-param", "dev")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "dev", res.Header.Get("x-test"))
	require.Equal(t, "test-ok", res.Header.Get("x-param-key"))
	require.True(t, scoped.AssertCalled(t))
}

func TestErrors(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test2")).
			Header("test", match.Function(
				func(_ any) (bool, error) {
					return false, fmt.Errorf("failed")
				})).
			Reply(OK()))

	res, err := http.Get(fmt.Sprintf("%s/test2", m.URL()))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.False(t, scoped.Called())
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestHTTPMockAppAssertions(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	ft := testmock.NewFakeT()

	scoped := m.MustMock(
		Get(match.URLPath("/test-ok")).
			Reply(OK()))

	require.EqualValues(t, 0, scoped.Hits())
	require.False(t, m.AssertCalled(ft))
	require.True(t, m.AssertNotCalled(ft))
	require.True(t, m.AssertNumberOfCalls(ft, 0))
	require.EqualValues(t, 0, m.Hits())

	res, err := http.Get(m.URL() + "/test-ok")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.EqualValues(t, 1, scoped.Hits())
	require.True(t, m.AssertCalled(ft))
	require.False(t, m.AssertNotCalled(ft))
	require.True(t, m.AssertNumberOfCalls(ft, 1))
	require.EqualValues(t, 1, m.Hits())
}

func TestHTTPMockAppEnableDisable(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(
		Get(match.URLPath("/test-1")).
			Reply(OK()),
		Get(match.URLPath("/test-2")).
			Reply(OK()))

	res, err := http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	// disable all mocks
	// should return tea pot for all
	m.Disable()

	res, err = http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	res, err = http.Get(m.URL() + "/test-2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	// re-enable mocks again
	m.Enable()

	res, err = http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL() + "/test-2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestHTTPMockAppSilently(t *testing.T) {
	m := MustNew(Setup().LogVerbosity(vlatte.LogBasic).LogLevel(vlatte.LogLevelNone))
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Reply(Created().
				PlainText("hello world")))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, 201, res.StatusCode)
	require.Equal(t, string(body), "hello world")
}

func TestSchemeMatching(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Scheme("http").
			Reply(OK()))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestHTTPMockAppNoReply(t *testing.T) {
	m := MustNew()

	scoped, err := m.Mock(Get(match.URLPath("/test")))
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestHTTPMockAppNoMatchers(t *testing.T) {
	m := MustNew()

	scoped, err := m.Mock(Request())
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestHTTPMockApp_RequestMatches(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			RequestMatches(func(r *http.Request) (bool, error) {
				if r.Method == http.MethodGet {
					return true, nil
				}

				return false, nil
			}).
			Reply(OK()))

	httpClient := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, http.StatusOK, res.StatusCode)

	req, _ = http.NewRequest(http.MethodPost, m.URL()+"/test", nil)
	res, err = httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	vlatte.Times(t, scoped, 1)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestHTTPMockAppConcurrency(t *testing.T) {
	m := MustNew()
	jobs := 10
	wg := sync.WaitGroup{}

	for i := range jobs {
		wg.Add(1)
		go func(index int) {
			m.MustMock(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustMock(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			m.Hits()
			m.Disable()
			m.Enable()

			m.Clean(t.Context())

			m.MustMock(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustMock(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			wg.Done()
		}(i)

		m.MustMock(Getf("%s", "/test-outside--"+strconv.FormatInt(int64(i), 10)).Reply(Created()))
		m.Clean(t.Context())

		m.Hits()
		m.Disable()
		m.Enable()
	}

	m.Hits()
	m.MustMock(Getf("/test-final").Reply(BadRequest()))
	m.MustStart()
	m.Close()

	wg.Wait()

	t.Log("done")
}

func TestHTTPMockAppConcurrentRequests(t *testing.T) {
	jobs := 20
	wg := sync.WaitGroup{}
	httpClient := &http.Client{}

	m := MustNew()
	m.MustStart()

	defer m.Close()

	// get must not stop the goroutine with require: FailNow is only valid on the test goroutine.
	get := func(path string) int {
		res, err := httpClient.Get(m.URL() + path)
		if !assert.NoError(t, err) {
			return 0
		}

		assert.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	for i := range jobs {
		wg.Go(func() {
			num := strconv.FormatInt(int64(i), 10)

			scope1 := m.MustMock(Getf("%s", "/test--"+num).Reply(OK()))
			scope2 := m.MustMock(Getf("%s", "/concurrency--"+num).Reply(Accepted()))
			scope3 := m.MustMock(Getf("%s", "/seq--"+num).Reply(Seq(OK(), BadRequest())))
			scope4 := m.MustMock(Getf("%s", "/rand--"+num).Reply(Rand(Accepted(), InternalServerError())))

			assert.Equal(t, http.StatusOK, get("/test--"+num))

			m.MustMock(Getf("%s", "/test-after--"+num).Reply(BadRequest()))

			assert.Equal(t, http.StatusAccepted, get("/concurrency--"+num))

			m.MustMock(Getf("%s", "/concurrency-after--"+num).Reply(InternalServerError()))

			assert.Equal(t, http.StatusOK, get("/seq--"+num))
			assert.Equal(t, http.StatusBadRequest, get("/seq--"+num))

			for range 50 {
				code := get("/rand--" + num)
				assert.True(t, code == http.StatusAccepted || code == http.StatusInternalServerError)
			}

			scope1.AssertCalled(t)
			vlatte.Times(t, scope1, 1)
			scope2.AssertCalled(t)
			vlatte.Times(t, scope2, 1)
			scope3.AssertCalled(t)
			vlatte.Times(t, scope3, 2)
			scope4.AssertCalled(t)
			vlatte.Times(t, scope4, 50)

			m.MustMock(Getf("%s", "/test-final--"+num).Reply(PartialContent()))
		})
	}

	wg.Wait()

	require.EqualValues(t, 54*jobs, m.Hits())
}

func TestDefaultLoggerIncludesServerName(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	log := (&Server{}).getLog(&Config{Name: "srv"})
	log.Info().Msg("hello")
	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	// Log lines must name the server, so the logs of several mock servers can be told apart.
	require.Contains(t, string(b), `"server":"srv"`)
}

func TestURL_InvalidPathPanicsInsteadOfExiting(t *testing.T) {
	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	// Path elements must already be escaped, so "50%" is invalid. A library must not exit the process.
	require.Panics(t, func() { m.URL("50%") })
}

func TestSettingOnlyPort(t *testing.T) {
	// Binding a custom address is covered by TestConfig; Port must produce a port-only address.
	m := MustNew(Setup().Port(12345))

	require.Equal(t, ":12345", m.Config().Addr)
}

func TestHTTPMockApp_Server(t *testing.T) {
	m := MustNew()
	srv := m.Backend()

	require.NotNil(t, srv)
	require.IsType(t, &httptestBackend{}, srv)
}

type failingConfigurer struct{}

func (failingConfigurer) Apply(*Config) error { return errors.New("bad config") }

type failingBackendFactory struct{}

func (failingBackendFactory) NewBackend(BackendConfig) (Backend, error) {
	return nil, errors.New("no server")
}

func TestNew_ReturnsErrors(t *testing.T) {
	_, err := New(failingConfigurer{})
	require.ErrorContains(t, err, "bad config")

	_, err = New(Setup().BackendFactory(failingBackendFactory{}))
	require.ErrorContains(t, err, "no server")

	require.Panics(t, func() { MustNew(failingConfigurer{}) })
}
