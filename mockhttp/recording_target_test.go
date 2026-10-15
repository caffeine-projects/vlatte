package mockhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// mockedServer starts a server whose every request a mock answers with "mock".
func mockedServer(t *testing.T, setup *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(setup)
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(OK().PlainText("mock")))

	return m
}

func TestRecording_TargetAnswersBeforeMocks(t *testing.T) {
	// A recording with a target sends it the requests it takes, though a mock matches them, and records its answers.
	// Once the recording stops, the mocks answer again.
	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "target "+r.URL.Path) })
	dir := t.TempDir()

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL)))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "target /a", string(body))

	res := stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, "target /a", onlyMock(t, dir).Response.Body)

	_, body = do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "mock", string(body))
}

func TestRecording_TakesWhatPassesItsFilters(t *testing.T) {
	// The recording takes the requests of its methods, in any case, and of its paths, whose * matches a "/" too, that
	// its filter matches. The target answers those; the mocks answer the others, which aren't recorded.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(
		RecordDir(t.TempDir()),
		RecordTarget(target.URL),
		RecordMethods("get", "POST"),
		RecordPaths("/v1/*", "/v2/items"),
		RecordFilter(Request().Header("X-Record", match.Equal("yes"))),
	))

	marked := http.Header{"X-Record": {"yes"}}

	tests := []struct {
		method string
		path   string
		header http.Header
		want   string
	}{
		{http.MethodGet, "/v1/a/b", marked, "target"},
		{http.MethodPost, "/v2/items", marked, "target"},
		{http.MethodDelete, "/v1/a", marked, "mock"},
		{http.MethodGet, "/v2/items/1", marked, "mock"},
		{http.MethodGet, "/v3", marked, "mock"},
		{http.MethodGet, "/v1/a", nil, "mock"},
		{http.MethodGet, "/v1/a", http.Header{"X-Record": {"no"}}, "mock"},
	}

	for _, tc := range tests {
		_, body := do(t, tc.method, m.URL(tc.path), tc.header, nil)
		require.Equal(t, tc.want, string(body), "%s %s %v", tc.method, tc.path, tc.header)
	}

	names := make([]string, 0, 2)
	for _, r := range stopRecording(t, m).Recordings {
		names = append(names, r.Name)
	}

	require.Equal(t, []string{"GET /v1/a/b", "POST /v2/items"}, names)
}

func TestRecording_FiltersWithoutATarget(t *testing.T) {
	// Without a target, the filters choose among what the server proxies.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })

	m, _ := recordingServer(t, target.URL, RecordPaths("/keep/*"))

	do(t, http.MethodGet, m.URL("/keep/a"), nil, nil)
	do(t, http.MethodGet, m.URL("/skip/a"), nil, nil)

	res := stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, "GET /keep/a", res.Recordings[0].Name)
}

func TestRecording_AFilterThatFailsTakesNothing(t *testing.T) {
	// A filter whose matcher fails, or panics, takes nothing: the mocks answer, and the failure is logged.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })

	for name, predicate := range map[string]func(*http.Request) (bool, error){
		"an error": func(*http.Request) (bool, error) { return false, errors.New("no") },
		"a panic":  func(*http.Request) (bool, error) { panic("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := mockedServer(t, Setup().Logger(&logger))
			require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordFilter(Request().RequestMatches(predicate))))

			res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "mock", string(body))

			require.Empty(t, stopRecording(t, m).Recordings)
			require.Contains(t, logs.String(), "recorder: a filter failed")
		})
	}
}

func TestRecording_AFilterWithAnErrorFailsTheStart(t *testing.T) {
	broken := Request().FormFile("f", FileWith().BodyFile("missing.bin"))

	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	err := m.StartRecording(RecordDir(t.TempDir()), RecordFilter(broken))
	require.ErrorContains(t, err, "recorder: filter: form_file f: open missing.bin")
	require.False(t, m.IsRecording())

	_, err = New(Setup().Record(RecordFilter(broken)))
	require.ErrorContains(t, err, "server: recorder: filter: form_file f")
}

func TestRecording_BadTargets(t *testing.T) {
	const notAbsolute = "expected an absolute http or https URL, such as https://api.example.com"

	const notBare = "expected no path, query or fragment: the requests recorded keep theirs"

	t.Run("credentials", func(t *testing.T) {
		// The requests recorded keep their credentials, and the target's never show in a message.
		m := MustNew(Setup().Silent())
		t.Cleanup(m.Close)

		err := m.StartRecording(RecordDir(t.TempDir()), RecordTarget("https://user:secret@api.example.com"))
		require.EqualError(t, err,
			`recorder: target "https://user:xxxxx@api.example.com": expected no user info: the requests recorded keep their credentials`)

		_, err = New(Setup().Record(RecordTarget("https://user:secret@api.example.com/v1")))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	})

	tests := map[string]string{
		"api.example.com":             notAbsolute,
		"/v1":                         notAbsolute,
		"ftp://api.example.com":       notAbsolute,
		"https://":                    notAbsolute,
		"https://api.example.com/v1":  notBare,
		"https://api.example.com?a=1": notBare,
		"https://api.example.com?":    notBare,
		"https://api.example.com#top": notBare,
	}

	for target, problem := range tests {
		m := MustNew(Setup().Silent())
		t.Cleanup(m.Close)

		require.EqualError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target)), `recorder: target "`+target+`": `+problem)

		_, err := New(Setup().Record(RecordTarget(target)))
		require.ErrorContains(t, err, problem, target)
	}

	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget("https://api.example.com/")), "a lone / is no path")
}

func TestRecording_ATargetItCantReach(t *testing.T) {
	// A target that can't be reached gets vlatte's error answer, which isn't recorded.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(gone.URL)))

	res, _ := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderXVlatteError))

	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_ATargetThatBreaksOff(t *testing.T) {
	// An answer the target cut short reaches the client cut short, and isn't recorded.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "100")
		_, _ = io.WriteString(w, "only part of it")
	})
	dir := t.TempDir()

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL)))

	if res, err := _rawClient.Get(m.URL("/a")); err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}

	require.Empty(t, stopRecording(t, m).Recordings)
	require.Empty(t, recordedMocks(t, dir))
}

func TestRecording_ATargetsRedirectIsPassedOn(t *testing.T) {
	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/new", http.StatusFound) })
	dir := t.TempDir()

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL)))

	res, _ := do(t, http.MethodGet, m.URL("/old"), nil, nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.Equal(t, "/new", res.Header.Get("Location"))

	stopRecording(t, m)

	mock := onlyMock(t, dir)
	require.Equal(t, http.StatusFound, mock.Response.Status)
	require.Equal(t, []string{"/new"}, mock.Response.Header["Location"])
}

func TestRecording_ATLSTarget(t *testing.T) {
	// A target serving TLS with a certificate no one signed, as test servers do, is recorded, unless the recording
	// verifies its certificate.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") }))
	t.Cleanup(target.Close)

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "secure", string(body))
	require.Len(t, stopRecording(t, m).Recordings, 1)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordSSLVerify(true)))

	res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderXVlatteError))
	require.Contains(t, string(body), "certificate")
	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_TargetConnectionsCloseAtStop(t *testing.T) {
	// A recording releases its connections to the target when it stops, and the next one can record another target.
	var closed atomic.Int64

	first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "first") }))
	first.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed.Add(1)
		}
	}
	first.Start()
	t.Cleanup(first.Close)

	second := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "second") })

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(first.URL)))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "first", string(body))
	require.Zero(t, closed.Load(), "the connection stays open while the recording runs")

	stopRecording(t, m)
	require.Eventually(t, func() bool { return closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(second.URL)))

	_, body = do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "second", string(body))
}

func TestRecording_ARecordingThatGaveWayReleasesItsTarget(t *testing.T) {
	// A recording whose StopRecording gave up waiting finishes on its own: the next recording releases its target.
	var closed atomic.Int64

	first := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "first") }))
	first.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed.Add(1)
		}
	}
	first.Start()
	t.Cleanup(first.Close)

	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(first.URL), RecordTransform(func(*RecordedMock) error {
		<-release
		return nil
	})))

	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := m.StopRecording(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)
	require.Eventually(t, func() bool { return m.StartRecording(RecordDir(t.TempDir())) == nil }, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
}

func TestRecording_StopDuringATargetRequest(t *testing.T) {
	// A request the target answers while the recording stops gets its answer whole: the recording doesn't take it.
	arrived := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		<-release
		_, _ = io.WriteString(w, "late")
	})
	dir := t.TempDir()

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL)))

	answered := make(chan string, 1)

	go func() {
		res, err := _rawClient.Get(m.URL("/a"))
		if err != nil {
			answered <- err.Error()
			return
		}

		defer res.Body.Close()

		b, _ := io.ReadAll(res.Body)
		answered <- string(b)
	}()

	<-arrived

	res, err := m.StopRecording(context.Background())
	require.NoError(t, err)
	require.Empty(t, res.Recordings)

	close(release)
	require.Equal(t, "late", <-answered)
	require.Empty(t, recordedMocks(t, dir))
}

func TestRecording_DescribeConfigNamesTheTarget(t *testing.T) {
	m := MustNew(Setup().Silent().Record(RecordDir("rec"), RecordTarget("https://api.example.com")))
	t.Cleanup(m.Close)

	require.Contains(t, m.DescribeConfig(), "Recording: rec\nRecording Target: https://api.example.com\n")
}
