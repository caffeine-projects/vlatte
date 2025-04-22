package xhttp

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte/drink/xhttp/test/testmock"
	. "github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/shelf/kv/kvmem"
	"github.com/caffeine-projects/vlatte/sip"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

func TestMocha(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(
		Get(URLPath("/test")).
			Header("test", StrictEqual("hello")).
			Query("filter", StrictEqual("all")).
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
	require.Equal(t, string(body), "hello world")
}

func TestResponseMapperModifyingResponse(t *testing.T) {
	const k = "key"
	const v = "test-ok"

	kv := kvmem.New()
	err := kv.Set(t.Context(), k, []byte(v))
	require.NoError(t, err)

	m := New(Setup().Parameters(kv))
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(Get(URLPath("/test")).
		Reply(OK()).
		Map(func(rv *RequestValues, r *MockedResponse) error {
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
	m := New()
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(
		Get(URLPath("/test2")).
			Header("test", Function(
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

func TestMochaAssertions(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	ft := testmock.NewFakeT()

	scoped := m.MustBrew(
		Get(URLPath("/test-ok")).
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

func TestMochaEnableDisable(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	m.MustBrew(
		Get(URLPath("/test-1")).
			Reply(OK()),
		Get(URLPath("/test-2")).
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

func TestMochaSilently(t *testing.T) {
	m := New(Setup().LogVerbosity(LogBasic).LogLevel(LogLevelNone))
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(
		Get(URLPath("/test")).
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
	m := New()
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(
		Get(URLPath("/test")).
			Scheme("http").
			Reply(OK()))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMochaNoReply(t *testing.T) {
	m := New()

	scoped, err := m.Brew(Get(URLPath("/test")))
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestMochaNoMatchers(t *testing.T) {
	m := New()

	scoped, err := m.Brew(Request())
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestMocha_RequestMatches(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	scoped := m.MustBrew(
		Get(URLPath("/test")).
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

	sip.Times(t, scoped, 1)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestMochaConcurrency(t *testing.T) {
	m := New()
	jobs := 10
	wg := sync.WaitGroup{}

	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(index int) {
			m.MustBrew(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustBrew(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			m.Hits()
			m.Disable()
			m.Enable()

			m.Clean(t.Context())

			m.MustBrew(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustBrew(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			wg.Done()
		}(i)

		m.MustBrew(Getf("%s", "/test-outside--"+strconv.FormatInt(int64(i), 10)).Reply(Created()))
		m.Clean(t.Context())

		m.Hits()
		m.Disable()
		m.Enable()
	}

	m.Hits()
	m.MustBrew(Getf("/test-final").Reply(BadRequest()))
	m.MustStart()
	m.Close()

	wg.Wait()

	t.Log("done")
}

func TestMochaConcurrentRequests(t *testing.T) {
	jobs := 20
	wg := sync.WaitGroup{}
	httpClient := &http.Client{}

	m := New()
	m.MustStart()

	defer m.Close()

	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(index int) {
			num := strconv.FormatInt(int64(index), 10)

			scope1 := m.MustBrew(Getf("%s", "/test--"+num).Reply(OK()))
			scope2 := m.MustBrew(Getf("%s", "/concurrency--"+num).Reply(Accepted()))
			scope3 := m.MustBrew(Getf("%s", "/seq--"+num).Reply(Seq(OK(), BadRequest())))
			scope4 := m.MustBrew(Getf("%s", "/rand--"+num).Reply(Rand(Accepted(), InternalServerError())))

			res, err := httpClient.Get(m.URL() + "/test--" + num)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)

			m.MustBrew(Getf("%s", "/test-after--"+num).Reply(BadRequest()))

			res, err = httpClient.Get(m.URL() + "/concurrency--" + num)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusAccepted, res.StatusCode)

			m.MustBrew(Getf("%s", "/concurrency-after--"+num).Reply(InternalServerError()))

			res, err = httpClient.Get(m.URL() + "/seq--" + num)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)

			res, err = httpClient.Get(m.URL() + "/seq--" + num)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusBadRequest, res.StatusCode)

			for n := 0; n < 50; n++ {
				res, err = httpClient.Get(m.URL() + "/rand--" + num)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.True(t, res.StatusCode == http.StatusAccepted || res.StatusCode == http.StatusInternalServerError)
			}

			scope1.AssertCalled(t)
			sip.Times(t, scope1, 1)
			scope2.AssertCalled(t)
			sip.Times(t, scope2, 1)
			scope3.AssertCalled(t)
			sip.Times(t, scope3, 2)
			scope4.AssertCalled(t)
			sip.Times(t, scope4, 50)

			m.MustBrew(Getf("%s", "/test-final--"+num).Reply(PartialContent()))

			wg.Done()
		}(i)
	}

	wg.Wait()

	require.EqualValues(t, 54*jobs, m.Hits())
}

func TestSettingOnlyPort(t *testing.T) {
	randomPort := func() int {
		l, err := net.Listen("tcp", ":0")
		require.NoError(t, err)
		require.NoError(t, l.Close())

		return l.Addr().(*net.TCPAddr).Port
	}

	port := randomPort()
	m := New(Setup().Port(port)).CloseWithT(t)
	m.MustStart()
	m.MustBrew(Getf("/test").Reply(OK()))

	hc := &http.Client{}
	res, err := hc.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, m.URL(), strconv.FormatInt(int64(port), 10))
}

func TestMocha_Server(t *testing.T) {
	m := New()
	srv := m.Server()

	require.NotNil(t, srv)
	require.IsType(t, &httpTestServer{}, srv)
	require.IsType(t, &httptest.Server{}, srv.S())
}
