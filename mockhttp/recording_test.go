package mockhttp

import (
	"bufio"
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte"
)

// newTarget starts a server for recordings to proxy to.
func newTarget(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	target := httptest.NewServer(h)
	t.Cleanup(target.Close)

	return target
}

// recordingServer starts a server that sends every request to target, and starts recording into a directory of its
// own, which it returns.
func recordingServer(t *testing.T, target string, options ...RecordConfigurer) (*Server, string) {
	t.Helper()

	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(From(target)))
	require.NoError(t, m.StartRecording(append([]RecordConfigurer{RecordDir(dir)}, options...)...))

	return m, dir
}

// stopRecording stops the recording of m, which must record every call it took, and returns what it wrote.
func stopRecording(t *testing.T, m *Server) *vlatte.RecordResult {
	t.Helper()

	res, err := m.StopRecording(t.Context())
	require.NoError(t, err)

	return res
}

// recordedMocks decodes the mock files of dir, by file name. A temporary file left behind fails the test.
func recordedMocks(t *testing.T, dir string) map[string]recordedMockFile {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	mocks := make(map[string]recordedMockFile)

	for _, e := range entries {
		name := e.Name()
		require.False(t, strings.HasSuffix(name, ".tmp"), "a temporary file is left: %s", name)

		ext := filepath.Ext(name)
		if !strings.HasSuffix(strings.TrimSuffix(name, ext), ".mock") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)

		if ext != ".json" {
			var doc any
			require.NoError(t, yaml.Unmarshal(data, &doc), name)

			data, err = json.Marshal(doc)
			require.NoError(t, err, name)
		}

		var m recordedMockFile
		require.NoError(t, json.Unmarshal(data, &m, json.RejectUnknownMembers(true)), name)

		mocks[name] = m
	}

	return mocks
}

// onlyMock returns the one mock file of dir.
func onlyMock(t *testing.T, dir string) recordedMockFile {
	t.Helper()

	mocks := recordedMocks(t, dir)
	require.Len(t, mocks, 1, "mock files: %v", slices.Collect(maps.Keys(mocks)))

	for _, m := range mocks {
		return m
	}

	return recordedMockFile{}
}

// replayServer starts a server that loads the mock files of dir only.
func replayServer(t *testing.T, dir string) *Server {
	t.Helper()

	m := MustNew(Setup().Silent().MockFilePatterns(
		filepath.Join(dir, "*.mock.json"), filepath.Join(dir, "*.mock.yaml"), filepath.Join(dir, "*.mock.yml")))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// do sends a request and reads its whole response. The client neither follows redirects nor asks for gzip itself, so
// it gets the answers as they are sent.
func do(t testing.TB, method, target string, header http.Header, body []byte) (*http.Response, []byte) {
	t.Helper()

	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, target, r)
	require.NoError(t, err)

	for k, values := range header {
		req.Header[k] = slices.Clone(values)
	}

	res, err := _rawClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// _rawClient gets answers as they are sent.
var _rawClient = &http.Client{
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// jsonOf writes v as JSON, one way, to compare sections whatever their Go types.
func jsonOf(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v, json.Deterministic(true))
	require.NoError(t, err)

	return string(b)
}

func TestRecording_StartsAndStops(t *testing.T) {
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(From(target.URL)))

	_, err := m.StopRecording(t.Context())
	require.ErrorIs(t, err, vlatte.ErrNotRecording, "nothing records yet")
	require.False(t, m.IsRecording())
	require.Equal(t, vlatte.RecordStatus{}, m.RecordingStatus())

	first := t.TempDir()
	require.NoError(t, m.StartRecording(RecordDir(first)))
	require.True(t, m.IsRecording())
	require.ErrorIs(t, m.StartRecording(RecordDir(t.TempDir())), vlatte.ErrRecording, "one recording at a time")

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, int64(1), m.RecordingStatus().Calls)

	res := stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, "GET /a", res.Recordings[0].Name)
	require.Equal(t, filepath.Dir(res.Recordings[0].File), first)
	require.False(t, res.Status.Active)
	require.Equal(t, int64(1), res.Status.Mocks)
	require.False(t, m.IsRecording())

	_, err = m.StopRecording(t.Context())
	require.ErrorIs(t, err, vlatte.ErrNotRecording, "the recording ended")

	// Another recording starts afresh, in a directory of its own.
	second := t.TempDir()
	require.NoError(t, m.StartRecording(RecordDir(second)))
	do(t, http.MethodGet, m.URL("/b"), nil, nil)

	res = stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.Equal(t, "GET /b", res.Recordings[0].Name)
	require.Len(t, recordedMocks(t, first), 1, "the first recording is left as it was")
}

func TestRecording_StopWaitsForItsContext(t *testing.T) {
	// A recording that can't write in time keeps writing: StopRecording gives up waiting, and a recording can't start
	// until the last one wrote what it took.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	m, dir := recordingServer(t, target.URL, RecordTransform(func(*RecordedMock) error {
		<-release
		return nil
	}))

	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := m.StopRecording(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, m.IsRecording(), "it takes no more calls")
	require.ErrorIs(t, m.StartRecording(RecordDir(t.TempDir())), vlatte.ErrRecording, "the last recording still writes")

	close(release)

	res := stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.Len(t, recordedMocks(t, dir), 1)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir())))
}

// closeOnce returns a cleanup that closes ch, unless it is closed already.
func closeOnce(ch chan struct{}) func() {
	return func() {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func TestRecording_ConfiguredRecordsFromStartToClose(t *testing.T) {
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dir := t.TempDir()

	m := MustNew(Setup().Silent().Record(RecordDir(dir)))
	require.False(t, m.IsRecording(), "it records once the server starts")

	m.MustStart()
	m.MustMock(AnyMethod().Reply(From(target.URL)))
	require.True(t, m.IsRecording())

	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	m.Close()
	require.Len(t, recordedMocks(t, dir), 1, "Close writes what the recording took")
	require.False(t, m.IsRecording())
}

func TestRecording_ConfiguredRecordsEachStart(t *testing.T) {
	// The recording the config sets runs from each start to close: StopRecording ends it until the next start, which
	// a reload isn't.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dir := t.TempDir()

	m := MustNew(Setup().Silent().Record(RecordDir(dir)))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(From(target.URL)))

	stopRecording(t, m)
	require.NoError(t, m.Reload())
	require.False(t, m.IsRecording())

	m.Close()
	m.MustStart()
	require.True(t, m.IsRecording(), "a restart records again")

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	m.Close()

	require.Len(t, recordedMocks(t, dir), 1)
}

func TestRecording_StartedBeforeTheServer(t *testing.T) {
	// A recording started before the server keeps going: the one the config sets doesn't fail the start.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	configured, started := t.TempDir(), t.TempDir()

	m := MustNew(Setup().Silent().Record(RecordDir(configured)))
	require.NoError(t, m.StartRecording(RecordDir(started)))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(From(target.URL)))

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)

	require.Len(t, recordedMocks(t, started), 1)
	require.Empty(t, recordedMocks(t, configured))
}

func TestRecording_BadSettings(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	tests := []struct {
		name    string
		options []RecordConfigurer
		err     string
	}{
		{
			name:    "a format mock files don't have",
			options: []RecordConfigurer{RecordFormat("toml")},
			err:     `recorder: format "toml" is not one of json, yaml, yml`,
		},
		{
			name:    "repeats it doesn't know",
			options: []RecordConfigurer{RecordRepeats(7)},
			err:     "recorder: repeats 7 is not vlatte.RecordSequence or vlatte.RecordFirst",
		},
		{
			name:    "a negative MaxInlineBody",
			options: []RecordConfigurer{RecordMaxInlineBody(-1)},
			err:     "recorder: MaxInlineBody must be 0 or more, not -1",
		},
		{
			name:    "no directory",
			options: []RecordConfigurer{RecordDir("")},
			err:     "recorder: no directory to record into",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+" fails New", func(t *testing.T) {
			_, err := New(Setup().Record(tc.options...))
			require.EqualError(t, err, "server: "+tc.err)
		})

		t.Run(tc.name+" fails StartRecording", func(t *testing.T) {
			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			require.EqualError(t, m.StartRecording(tc.options...), tc.err)
			require.False(t, m.IsRecording())
		})
	}

	t.Run("an option that fails fails StartRecording", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		t.Cleanup(m.Close)

		require.EqualError(t, m.StartRecording(failingRecordOption{}), "recorder: option at index 0 with type mockhttp.failingRecordOption: no")
		require.False(t, m.IsRecording())
	})

	t.Run("a directory it can't make fails StartRecording", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		t.Cleanup(m.Close)

		require.ErrorContains(t, m.StartRecording(RecordDir(filepath.Join(notADir, "sub"))), "not a directory")
		require.False(t, m.IsRecording())
	})

	t.Run("the format is written as mock files name it", func(t *testing.T) {
		m, err := New(Setup().Silent().Record(RecordFormat(".YAML"), RecordDir(t.TempDir())))
		require.NoError(t, err)
		require.Equal(t, "yaml", m.Config().Record.Format)
	})
}

type failingRecordOption struct{}

func (failingRecordOption) Apply(*RecordConfig) error { return errors.New("no") }

func TestRecordConfig_Compose(t *testing.T) {
	// Each option changes its own setting: a config given as an option sets what it has, and transforms add up.
	var calls []string

	transform := func(name string) func(*RecordedMock) error {
		return func(*RecordedMock) error {
			calls = append(calls, name)
			return nil
		}
	}

	c := defaultRecordConfig()
	for _, option := range []RecordConfigurer{
		RecordDir("a"),
		RecordRequestHeaders("Accept"),
		RecordTransform(transform("first")),
		&RecordConfig{Format: "yaml", MaxBodySize: -1, Transforms: []func(*RecordedMock) error{transform("second")}},
		RecordTransform(transform("third")),
		&RecordConfig{IncludeMocked: true},
	} {
		require.NoError(t, option.Apply(c))
	}

	require.Equal(t, "a", c.Dir)
	require.Equal(t, "yaml", c.Format)
	require.Equal(t, []string{"Accept"}, c.RequestHeaders, "a config without headers keeps them")
	require.Equal(t, int64(-1), c.MaxBodySize)
	require.Equal(t, int64(_defaultMaxInlineBody), c.MaxInlineBody)
	require.True(t, c.IncludeMocked)

	for _, tr := range c.Transforms {
		require.NoError(t, tr(nil))
	}

	require.Equal(t, []string{"first", "second", "third"}, calls)

	// A recording started from the server's config doesn't change it.
	base := &RecordConfig{Transforms: make([]func(*RecordedMock) error, 1, 4)}
	base.Transforms[0] = transform("base")

	m := MustNew(Setup().Silent().Record(base, RecordDir(t.TempDir())))
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordTransform(transform("more")), RecordRequestHeaders("X-A")))
	require.Len(t, m.Config().Record.Transforms, 1)
	require.Empty(t, m.Config().Record.RequestHeaders)
}

func TestRecording_DirStartsAtRootDir(t *testing.T) {
	root := t.TempDir()
	abs := t.TempDir()

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	m := MustNew(Setup().Silent().RootDir(root).Record(RecordDir("rec")))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(From(target.URL)))

	require.Contains(t, m.DescribeConfig(), "Recording: "+filepath.Join(root, "rec")+"\n")

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)
	require.Len(t, recordedMocks(t, filepath.Join(root, "rec")), 1, "a relative directory starts at the root")

	require.NoError(t, m.StartRecording(RecordDir(abs)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)
	require.Len(t, recordedMocks(t, abs), 1, "an absolute one is used as it is")
}

func TestRecording_RecordsProxiedAnswers(t *testing.T) {
	// A mock's own answer is what the mock file says already: only answers from elsewhere are recorded, unless asked.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "from the target") })

	for _, includeMocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("IncludeMocked %v", includeMocked), func(t *testing.T) {
			dir := t.TempDir()

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(
				Getf("/proxied").Reply(From(target.URL)),
				Getf("/mocked").Reply(OK().PlainText("from the mock")),
				Getf("/sequence").Reply(Seq().Add(From(target.URL))),
			)

			require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(includeMocked)))

			for _, path := range []string{"/proxied", "/mocked", "/sequence"} {
				do(t, http.MethodGet, m.URL(path), nil, nil)
			}

			names := make([]string, 0, 3)
			for _, r := range stopRecording(t, m).Recordings {
				names = append(names, r.Name)
			}

			if includeMocked {
				require.Equal(t, []string{"GET /proxied", "GET /mocked", "GET /sequence"}, names)
			} else {
				require.Equal(t, []string{"GET /proxied", "GET /sequence"}, names, "a proxy in a sequence still proxies")
			}
		})
	}
}

func TestRecording_RepliesWithoutAResponse(t *testing.T) {
	// #24: replies that write their own response, such as Echo, Handler and Func, are recorded as the client got them.
	logs := &logBuffer{}
	logger := zerolog.New(logs)
	dir := t.TempDir()

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Postf("/echo").Reply(Echo()),
		Getf("/handler").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentType, MIMETextPlain)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, "from a handler")
		})),
		Getf("/func").Reply(Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
			_, err := io.WriteString(w, "from a func")
			return nil, err
		})),
	)

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	do(t, http.MethodPost, m.URL("/echo"), http.Header{HeaderContentType: {MIMETextPlain}}, []byte("echoed"))
	do(t, http.MethodGet, m.URL("/handler"), nil, nil)
	do(t, http.MethodGet, m.URL("/func"), nil, nil)

	stopRecording(t, m)

	replay := replayServer(t, dir)

	res, body := do(t, http.MethodGet, replay.URL("/handler"), nil, nil)
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, "from a handler", string(body))

	_, body = do(t, http.MethodGet, replay.URL("/func"), nil, nil)
	require.Equal(t, "from a func", string(body))

	_, body = do(t, http.MethodPost, replay.URL("/echo"), http.Header{HeaderContentType: {MIMETextPlain}}, []byte("echoed"))
	require.Contains(t, string(body), "echoed")

	require.NotContains(t, logs.String(), "panic")
}

func TestRecording_ForwardProxy(t *testing.T) {
	// The forward proxy answers requests no mock matches with their host's answer, which is recorded. A host it
	// can't reach gets its error answer, which isn't.
	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderContentType, MIMETextPlain)
		_, _ = io.WriteString(w, "host "+r.URL.Path)
	})

	dir := t.TempDir()

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordDir(dir)))

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL + "/items?page=2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	res, err = client.Get(closed.URL + "/gone")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)

	result := stopRecording(t, m)
	require.Len(t, result.Recordings, 1)

	mock := onlyMock(t, dir)
	require.Equal(t, "/items?page=2", mock.Request.URL)
	require.Equal(t, "host /items", mock.Response.Body)
}

func TestRecording_FailedAnswersAreNotRecorded(t *testing.T) {
	// Only an answer the client got whole is recorded: vlatte's own answers to a failure aren't.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	half := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "100")
		_, _ = io.WriteString(w, "only part of it")
	})

	tests := []struct {
		name string
		mock *Builder
	}{
		{"no mock matches", Getf("/other").Reply(OK())},
		{"a target it can't reach", Getf("/a").Reply(From(gone.URL))},
		{"a body cut short", Getf("/a").Reply(From(half.URL))},
		{"a failing mapper", Getf("/a").Reply(OK()).Map(func(*RequestValues, *Response) error { return errors.New("no") })},
		{"a reply that panics", Getf("/a").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { panic("boom") }))},
		{"a reply that fails", Getf("/a").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
			return nil, errors.New("no")
		}))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(tc.mock)
			require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/a"), nil)
			require.NoError(t, err)

			if res, err := http.DefaultClient.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				require.NoError(t, res.Body.Close())
			}

			res := stopRecording(t, m)
			require.Empty(t, res.Recordings)
			require.Empty(t, recordedMocks(t, dir))
		})
	}
}

func TestRecording_AClientThatLeavesIsNotRecorded(t *testing.T) {
	// An answer the client didn't get whole isn't recorded, though the reply sent it without an error.
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/big").Reply(OK().Body(bytes.Repeat([]byte("x"), 64<<20))))
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	conn, err := net.Dial("tcp", strings.TrimPrefix(m.URL(), "http://"))
	require.NoError(t, err)

	_, err = io.WriteString(conn, "GET /big HTTP/1.1\r\nHost: vlatte\r\n\r\n")
	require.NoError(t, err)

	_, err = conn.Read(make([]byte, 1))
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	// The mock counts the request once it answered it, after the recording took it, or not.
	require.Eventually(t, func() bool { return m.Hits() == 1 }, 10*time.Second, 10*time.Millisecond)

	require.Empty(t, stopRecording(t, m).Recordings)
	require.Empty(t, recordedMocks(t, dir))
}

func TestRecording_DoesNotDelayResponses(t *testing.T) {
	// #33: a response never waits for its recording, nor for the recordings before it.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	m, dir := recordingServer(t, target.URL, RecordTransform(func(*RecordedMock) error {
		<-release
		return nil
	}))

	start := time.Now()

	for i := range 50 {
		res, _ := do(t, http.MethodGet, m.URL(fmt.Sprintf("/fast/%d", i)), nil, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
	}

	require.Less(t, time.Since(start), 5*time.Second, "the responses waited for the recording")

	close(release)

	require.Len(t, stopRecording(t, m).Recordings, 50)
	require.Len(t, recordedMocks(t, dir), 50)
}

func TestRecording_ConnectIsNotCaptured(t *testing.T) {
	// A CONNECT opens a tunnel, which answers nothing a mock could: it isn't captured, and the tunnel works.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through the tunnel")
	}))
	t.Cleanup(target.Close)

	dir := t.TempDir()

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordDir(dir)))

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)

	client := &http.Client{Transport: transport}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "through the tunnel", string(body))

	client.CloseIdleConnections()

	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_HijackedConnectionsAreNotRecorded(t *testing.T) {
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/raw").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 3\r\nConnection: close\r\n\r\nraw")
		_ = rw.Flush()
	})))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	res, body := do(t, http.MethodGet, m.URL("/raw"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "raw", string(body))

	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_StreamingAnswersStillStream(t *testing.T) {
	// A recording sees the answer as it streams: the client gets each part as the target sends it.
	sent := make(chan struct{})
	more := make(chan struct{})

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		http.NewResponseController(w).Flush()
		close(sent)
		<-more
		_, _ = io.WriteString(w, "data: last\n\n")
	})

	m, dir := recordingServer(t, target.URL)

	res, err := http.Get(m.URL("/events"))
	require.NoError(t, err)

	r := bufio.NewReader(res.Body)

	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", line, "the first part arrived before the target sent the last")

	<-sent
	close(more)

	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "\ndata: last\n\n", string(rest))

	stopRecording(t, m)
	require.Equal(t, "data: first\n\ndata: last\n\n", onlyMock(t, dir).Response.Body)
}
