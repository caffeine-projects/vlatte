package mockhttp

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/jsontext"
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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
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

func TestRecording_ConfiguredRunsFromStartToClose(t *testing.T) {
	// The recording the config sets runs from start to close, unless StopRecording ends it first: a reload doesn't
	// start it again.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dir := t.TempDir()

	m := MustNew(Setup().Silent().Record(RecordDir(dir)))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Reply(From(target.URL)))
	require.True(t, m.IsRecording())

	stopRecording(t, m)
	require.NoError(t, m.Reload())
	require.False(t, m.IsRecording(), "a reload doesn't start it again")

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	m.Close()

	require.Empty(t, recordedMocks(t, dir), "a call after the stop isn't recorded")
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
		{
			name:    "an empty method",
			options: []RecordConfigurer{RecordMethods("GET", " ")},
			err:     "recorder: method 1 is empty",
		},
		{
			name:    "an empty path",
			options: []RecordConfigurer{RecordPaths("")},
			err:     "recorder: path 0 is empty",
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

	t.Run("a directory that is a file fails the start", func(t *testing.T) {
		m := MustNew(Setup().Silent().Record(RecordDir(notADir)))
		t.Cleanup(m.Close)

		require.ErrorContains(t, m.Start(), "loader: recordings "+notADir)
	})

	t.Run("the format is written as mock files name it, and lists without spaces", func(t *testing.T) {
		m, err := New(Setup().Silent().Record(RecordFormat(".YAML"), RecordDir(t.TempDir()), RecordMethods(" post "), RecordPaths(" /v1/* ")))
		require.NoError(t, err)
		require.Equal(t, "yaml", m.Config().Record.Format)
		require.Equal(t, []string{"post"}, m.Config().Record.Methods)
		require.Equal(t, []string{"/v1/*"}, m.Config().Record.Paths)
	})

	t.Run("a filter that fails after New fails the start", func(t *testing.T) {
		filter := Request()

		m := MustNew(Setup().Silent().Record(RecordDir(t.TempDir()), RecordFilter(filter)))
		t.Cleanup(m.Close)

		filter.FormFile("f", FileWith().BodyFile("missing.bin"))
		require.ErrorContains(t, m.Start(), "recorder: filter: form_file f")
		require.False(t, m.IsRecording())
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

	filter := Request().Header("X-A", match.Equal("1"))

	c := defaultRecordConfig()
	for _, option := range []RecordConfigurer{
		RecordDir("a"),
		RecordRequestHeaders("Accept"),
		RecordMethods("GET"),
		RecordTransform(transform("first")),
		&RecordConfig{
			Format: "yaml", Target: "https://api.example.com", MaxBodySize: -1, Paths: []string{"/v1/*"},
			Transforms: []func(*RecordedMock) error{transform("second")},
		},
		RecordTransform(transform("third")),
		&RecordConfig{IncludeMocked: true, Filter: filter},
	} {
		require.NoError(t, option.Apply(c))
	}

	require.Equal(t, "a", c.Dir)
	require.Equal(t, "yaml", c.Format)
	require.Equal(t, "https://api.example.com", c.Target)
	require.Equal(t, []string{"GET"}, c.Methods, "a config without methods keeps them")
	require.Equal(t, []string{"/v1/*"}, c.Paths)
	require.Same(t, filter, c.Filter)
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

func TestRecordConfig_ApplyCopiesTheLists(t *testing.T) {
	// A config given as an option sets its lists as copies: the caller's lists stay the caller's.
	option := &RecordConfig{
		Methods:                []string{"GET"},
		Paths:                  []string{"/v1/*"},
		RequestHeaders:         []string{"Accept"},
		ExcludeResponseHeaders: []string{"Set-Cookie"},
	}

	c := defaultRecordConfig()
	require.NoError(t, option.Apply(c))

	option.Methods[0], option.Paths[0], option.RequestHeaders[0], option.ExcludeResponseHeaders[0] = "x", "x", "x", "x"

	require.Equal(t, []string{"GET"}, c.Methods)
	require.Equal(t, []string{"/v1/*"}, c.Paths)
	require.Equal(t, []string{"Accept"}, c.RequestHeaders)
	require.Equal(t, []string{"Set-Cookie"}, c.ExcludeResponseHeaders)
}

func TestRecordConfig_SetsEitherRepeats(t *testing.T) {
	// A config given as an option sets the repeats it names, the default ones too, and keeps them when it names none.
	c := defaultRecordConfig()

	require.NoError(t, (&RecordConfig{Repeats: vlatte.RecordFirst}).Apply(c))
	require.Equal(t, vlatte.RecordFirst, c.Repeats)

	require.NoError(t, (&RecordConfig{}).Apply(c))
	require.Equal(t, vlatte.RecordFirst, c.Repeats, "a config without repeats keeps them")

	require.NoError(t, (&RecordConfig{Repeats: vlatte.RecordSequence}).Apply(c))
	require.Equal(t, vlatte.RecordSequence, c.Repeats)

	// A recording started over a server's config of RecordFirst.
	m := MustNew(Setup().Silent().Record(RecordDir(t.TempDir()), RecordRepeats(vlatte.RecordFirst)))
	t.Cleanup(m.Close)

	require.NoError(t, m.StartRecording(&RecordConfig{Repeats: vlatte.RecordSequence}))
	require.Equal(t, vlatte.RecordSequence, m.recording.Load().conf.Repeats)
}

func TestRecording_OptionsLeaveTheCallersListsAsTheyAre(t *testing.T) {
	// The recording trims the methods and paths it takes into lists of its own: a list of the caller, which other
	// recordings may share, stays as it was.
	paths := []string{" /v1/* "}
	methods := []string{" get "}

	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordPaths(paths...), RecordMethods(methods...)))
	require.Equal(t, []string{" /v1/* "}, paths)
	require.Equal(t, []string{" get "}, methods)
}

func TestRecording_StopAndStartAtOnce(t *testing.T) {
	// A StopRecording and a StartRecording at the same time: whichever ends the old recording, the stop returns, and
	// the start starts once the old one is done.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	dir := t.TempDir()

	for range 300 {
		require.NoError(t, m.StartRecording(RecordDir(dir)))

		var wg sync.WaitGroup

		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err := m.StopRecording(ctx)
			assert.NotErrorIs(t, err, context.DeadlineExceeded, "the stop waited for a recording no one ends")
		})

		wg.Go(func() {
			for m.StartRecording(RecordDir(dir)) != nil {
				runtime.Gosched()
			}
		})

		wg.Wait()

		_, err := m.StopRecording(t.Context())
		require.NoError(t, err)
	}
}

func TestRecording_TwoStartsAfterARecordingThatGaveWay(t *testing.T) {
	// A recording whose StopRecording gave up, and that wrote everything since: two StartRecording at once end it
	// once, and one starts. The other doesn't stop the one that started.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	dir := t.TempDir()

	for range 50 {
		release := make(chan struct{})

		require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL), RecordTransform(func(*RecordedMock) error {
			<-release
			return nil
		})))
		do(t, http.MethodGet, m.URL("/a"), nil, nil)

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		_, err := m.StopRecording(ctx)
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)

		close(release)
		<-m.recording.Load().session.Done()

		errs := make(chan error, 2)

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { errs <- m.StartRecording(RecordDir(dir)) })
		}

		wg.Wait()
		close(errs)

		started := 0
		for err := range errs {
			if err == nil {
				started++
			} else {
				require.ErrorIs(t, err, vlatte.ErrRecording)
			}
		}

		require.Equal(t, 1, started)
		require.True(t, m.IsRecording(), "the recording that started still runs")

		_, err = m.StopRecording(t.Context())
		require.NoError(t, err)
	}
}

func TestRecording_AStartServesARecordingWhoseStopGaveUp(t *testing.T) {
	// A StopRecording asks for the recordings to be served: when it gives up waiting, the StartRecording that ends the
	// recording later serves them. A StopRecording that the start came before returns once they are served.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })

	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordTransform(func(*RecordedMock) error {
		<-release
		return nil
	})))

	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := m.StopRecording(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	old := m.recording.Load()
	close(release)
	<-old.session.Done()

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir())))

	res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "ok", string(body), "the recording answers: no mock or target would")

	// The end of a StopRecording that got past its stop after the start.
	stopped, err := m.end(t.Context(), old, true)
	require.NoError(t, err)
	require.Len(t, stopped.Recordings, 1)
}

func TestRecording_AStopThatCloseCameBeforeSaysSo(t *testing.T) {
	// A StopRecording that Close comes before can't serve the recordings: it says so, rather than reporting success.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	m, _ := recordingServer(t, target.URL)

	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	rec := m.recording.Load()
	rec.serve.Store(true) // what the StopRecording does first

	_, _ = m.end(t.Context(), rec, false) // Close ends it

	_, err := m.end(t.Context(), rec, true) // then the StopRecording gets past its stop
	require.EqualError(t, err, "recorder: the server closed before serving the recordings")
	require.False(t, m.IsRecording())
}

func TestRecording_ATimedOutStopTakesNoMoreRequests(t *testing.T) {
	// A recording whose StopRecording gave up waiting still writes, but takes no more requests: they go to the
	// mocks, not to its target.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })

	release := make(chan struct{})
	t.Cleanup(closeOnce(release))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/a").Reply(OK().PlainText("mock")))

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordTransform(func(*RecordedMock) error {
		<-release
		return nil
	})))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "target", string(body))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := m.StopRecording(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	_, body = do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "mock", string(body))

	// The recording finishes before its directory goes.
	close(release)

	_, err = m.StopRecording(t.Context())
	require.NoError(t, err)
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
	do(t, http.MethodGet, m.URL("/b"), nil, nil)
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
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))

	result := stopRecording(t, m)
	require.Len(t, result.Recordings, 1)

	mock := onlyMock(t, dir)
	require.Equal(t, "/items?page=2", mock.Request.URL)
	require.Equal(t, "host /items", mock.Response.Body)
}

func TestRecording_AHostThatBreaksOffIsNotRecorded(t *testing.T) {
	// The forward proxy passes on what the host sends: a body the host cuts short isn't its answer, and isn't recorded.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "100")
		_, _ = io.WriteString(w, "part of it")
		http.NewResponseController(w).Flush()
		panic(http.ErrAbortHandler)
	})

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir())))

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	t.Cleanup(client.CloseIdleConnections)

	// The proxy cuts the client's connection: before the status, for a short body the server hadn't sent yet, or else
	// in the body.
	res, err := client.Get(target.URL + "/cut")
	if err == nil {
		_, err = io.ReadAll(res.Body)
		require.NoError(t, res.Body.Close())
	}

	require.Error(t, err, "the client doesn't take part of the body for all of it")

	result := stopRecording(t, m)
	require.Empty(t, result.Recordings)
	require.Zero(t, result.Status.Calls)
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

func TestRecording_APipeThatFailsIsNotRecorded(t *testing.T) {
	// A pipe that fails before the body goes out leaves the client without it: it isn't recorded either.
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/slow").Pipe(vlatte.PipeFunc(func(ctx context.Context, _ []byte, _ vlatte.Stage) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})).Reply(OK().PlainText("hello")))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/slow"), nil)
	require.NoError(t, err)

	if res, err := http.DefaultClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}

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

func TestRecording_HandlersKeepTheWriterFeatures(t *testing.T) {
	// A handler gets the features of the server's writer while it is recorded: it flushes, and sets deadlines.
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/h").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, _ = io.WriteString(w, "first, ")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "last")
	})))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	res, body := do(t, http.MethodGet, m.URL("/h"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "first, last", string(body))

	stopRecording(t, m)
	require.Equal(t, "first, last", onlyMock(t, dir).Response.Body)
}

func TestRecording_ADiskThatFailsLosesTheRecordingNotTheAnswer(t *testing.T) {
	// A body that goes to a file as it streams, on a disk that refuses it, isn't recorded: the client still gets every
	// byte of it.
	big := bytes.Repeat([]byte("0123456789"), 4<<10)
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(big) })

	m, dir := recordingServer(t, target.URL, RecordMaxInlineBody(1<<10))

	// A file where the directory goes: the body has nowhere to go.
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.WriteFile(dir, nil, 0o600))

	_, body := do(t, http.MethodGet, m.URL("/big"), nil, nil)
	require.Equal(t, big, body)

	res, err := m.StopRecording(t.Context())
	require.ErrorContains(t, err, "recorder: GET /big:")
	require.Empty(t, res.Recordings)
	require.Equal(t, int64(1), res.Status.Failed)
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

func TestRecording_ASwitchedConnectionIsNotRecorded(t *testing.T) {
	// A call that switched protocols through a proxy reply, here a mock file's, has no response to record: the
	// connection belongs to the client and the target from then on.
	target := switchingTarget(t, "echo")
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(FromBytes([]byte("request: {path: /ws}\nproxy: {target: '"+target.URL+"'}\n"), "yaml"))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	conn, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	_, err := io.WriteString(conn, "switched\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "switched\n", line)
	require.NoError(t, conn.Close())

	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_InterceptedHTTPS(t *testing.T) {
	// The requests of an intercepted HTTPS connection are the server's own: those no mock matches get their host's
	// answer, which is recorded, as for a request in absolute form.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderContentType, MIMETextPlain)
		_, _ = io.WriteString(w, "host "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	dir := t.TempDir()

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)
	require.NoError(t, m.StartRecording(RecordDir(dir)))

	res, err := interceptClient(t, m).Get(target.URL + "/items?page=2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	result := stopRecording(t, m)
	require.Len(t, result.Recordings, 1)

	mock := onlyMock(t, dir)
	require.Equal(t, "/items?page=2", mock.Request.URL)
	require.Equal(t, "host /items", mock.Response.Body)
}

// answering starts a target whose answer can change.
func answering(t *testing.T, answer string) (*atomic.Pointer[string], string) {
	t.Helper()

	var current atomic.Pointer[string]
	current.Store(&answer)

	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, *current.Load()+" "+r.URL.RequestURI())
	})

	return &current, target.URL
}

func TestPlayback_RecordingsAnswerOnceStopped(t *testing.T) {
	// Once the recording stops, the server answers what it recorded without the target, its body files included. A
	// request it didn't record gets the usual answer: no mock matches it.
	current, target := answering(t, "recorded")
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target), RecordBodyFiles(true)))
	do(t, http.MethodGet, m.URL("/items?page=2"), nil, nil)
	stopRecording(t, m)

	changed := "changed"
	current.Store(&changed)

	res, body := do(t, http.MethodGet, m.URL("/items?page=2"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "recorded /items?page=2", string(body))

	res, _ = do(t, http.MethodGet, m.URL("/items?page=3"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestPlayback_RecordingsComeBeforeAProxyOfLowerPriority(t *testing.T) {
	// A mock that proxies every request answers after the recordings when its priority number is higher: the requests
	// recorded are answered by their recordings, the others still go to the target.
	current, target := answering(t, "recorded")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Priority(10).Reply(From(target)))

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir())))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)

	changed := "live"
	current.Store(&changed)

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "recorded /a", string(body))

	_, body = do(t, http.MethodGet, m.URL("/b"), nil, nil)
	require.Equal(t, "live /b", string(body))
}

func TestPlayback_ARestartServesTheRecordings(t *testing.T) {
	// Recordings are mock files: a server started later serves them, from the default directory through the default
	// patterns, or from the directory its config records into.
	t.Chdir(t.TempDir())

	_, target := answering(t, "recorded")

	tests := []struct {
		name   string
		record []RecordConfigurer
		serve  *ConfigBuilder
	}{
		{"the default directory", nil, Setup()},
		{"the directory of the config", []RecordConfigurer{RecordDir("rec")}, Setup().Record(RecordDir("rec"))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := MustNew(Setup().Silent().Record(append([]RecordConfigurer{RecordTarget(target)}, tc.record...)...))
			recorder.MustStart()
			do(t, http.MethodGet, recorder.URL("/"+strings.ReplaceAll(tc.name, " ", "-")), nil, nil)
			recorder.Close()

			m := MustNew(tc.serve.Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			res, body := do(t, http.MethodGet, m.URL("/"+strings.ReplaceAll(tc.name, " ", "-")), nil, nil)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "recorded /"+strings.ReplaceAll(tc.name, " ", "-"), string(body))
		})
	}
}

func TestPlayback_AnAbsoluteDirectoryWithARootDir(t *testing.T) {
	// #20: an absolute record directory is used as it is, whatever the root directory.
	_, target := answering(t, "recorded")
	root, dir := t.TempDir(), t.TempDir()

	recorder := MustNew(Setup().Silent().RootDir(root).Record(RecordDir(dir), RecordTarget(target)))
	recorder.MustStart()
	do(t, http.MethodGet, recorder.URL("/a"), nil, nil)
	recorder.Close()

	require.Len(t, recordedMocks(t, dir), 1)

	m := MustNew(Setup().Silent().RootDir(root).Record(RecordDir(dir)))
	m.MustStart()
	t.Cleanup(m.Close)

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "recorded /a", string(body))
}

func TestPlayback_AFileLoadsOnce(t *testing.T) {
	// The default patterns and the record directory name the same files, under a relative and an absolute name: each
	// loads once. A record directory that doesn't exist yet loads nothing.
	t.Chdir(t.TempDir())

	_, target := answering(t, "recorded")

	dir, err := filepath.Abs(filepath.Join("testdata", "vlatte", "_mocks_recorded"))
	require.NoError(t, err)

	m := MustNew(Setup().Silent().Record(RecordTarget(target), RecordDir(dir)))
	m.MustStart()
	t.Cleanup(m.Close)

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)
	require.NoError(t, m.Reload())

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "recorded /a", string(body))

	missing := MustNew(Setup().Silent().Record(RecordDir("missing")))
	require.NoError(t, missing.Start())
	t.Cleanup(missing.Close)
}

func TestPlayback_ALinkedDirectoryLoadsOnce(t *testing.T) {
	// The patterns name the recordings through a link to the record directory: each file loads once.
	_, target := answering(t, "recorded")
	dir := t.TempDir()

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(link, "*.mock.json")).Record(RecordDir(dir), RecordTarget(target)))
	m.MustStart()
	t.Cleanup(m.Close)

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "recorded /a", string(body))
}

func TestPlayback_RecordingsThatShareAnIDWithAMockFile(t *testing.T) {
	// The same request recorded into two directories the server serves has one ID twice: StopRecording reports it,
	// with what it recorded, and the server serves the mocks it had.
	_, target := answering(t, "recorded")
	configured, other := t.TempDir(), t.TempDir()

	m := MustNew(Setup().Silent().Record(RecordDir(configured), RecordTarget(target)))
	m.MustStart()
	t.Cleanup(m.Close)

	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)

	require.NoError(t, m.StartRecording(RecordDir(other)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	res, err := m.StopRecording(t.Context())
	require.ErrorContains(t, err, "recorder: serving the recordings")
	require.ErrorContains(t, err, "mock id already in use")
	require.Len(t, res.Recordings, 1)

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "recorded /a", string(body))
	require.NoError(t, m.Reload(), "the recordings that failed to load don't break the next reload")
}

func TestPlayback_ARequestARecordingAnswersIsRecordedAgainOnlyThroughATarget(t *testing.T) {
	// A recording that is served answers its request before any proxy: a later recording that proxies takes the new
	// requests only. A recording with a target sends it every request it takes, so it records them again.
	_, target := answering(t, "recorded")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(AnyMethod().Priority(10).Reply(From(target)))

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir())))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	stopRecording(t, m)

	proxying := t.TempDir()
	require.NoError(t, m.StartRecording(RecordDir(proxying)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	do(t, http.MethodGet, m.URL("/b"), nil, nil)

	names := make([]string, 0, 1)
	for _, r := range stopRecording(t, m).Recordings {
		names = append(names, r.Name)
	}

	require.Equal(t, []string{"GET /b"}, names, "the recording answered /a")

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Len(t, stopRecording(t, m).Recordings, 1, "the target answered /a")
}

func TestPlayback_EveryStopRecordingReturnsOnceTheRecordingsAreServed(t *testing.T) {
	// Two StopRecording at once: one serves the recordings, and the other returns once it did.
	_, target := answering(t, "recorded")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	var wg sync.WaitGroup

	statuses := make(chan int, 2)

	for range 2 {
		wg.Go(func() {
			// One that comes once the other is done finds no recording: it is served already.
			if _, err := m.StopRecording(context.Background()); err != nil && !errors.Is(err, vlatte.ErrNotRecording) {
				statuses <- 0
				return
			}

			res, err := _rawClient.Get(m.URL("/a"))
			if err != nil {
				statuses <- 0
				return
			}

			_ = res.Body.Close()
			statuses <- res.StatusCode
		})
	}

	wg.Wait()
	close(statuses)

	for status := range statuses {
		require.Equal(t, http.StatusOK, status, "a StopRecording returned before the recordings were served")
	}
}

func TestPlayback_CloseServesNothingMore(t *testing.T) {
	// Close writes what the recording took, but loads nothing: the server is closing.
	_, target := answering(t, "recorded")
	dir := t.TempDir()

	m := MustNew(Setup().Silent())
	m.MustStart()
	require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target)))
	do(t, http.MethodGet, m.URL("/a"), nil, nil)

	m.Close()
	require.Len(t, recordedMocks(t, dir), 1)

	mocks, err := m.storage.List(t.Context())
	require.NoError(t, err)

	for mock := range mocks {
		require.NotContains(t, mock.ID(), "GET-a", "Close loaded the recordings")
	}
}

func TestPropRecording_RecordStopReplay(t *testing.T) {
	// The server that recorded answers each call again, once the recording stops, as the target did.
	var current atomic.Pointer[callModel]

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { current.Load().answer(w) })

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	base := t.TempDir()
	n := 0

	rapid.Check(t, func(rt *rapid.T) {
		c := genCall().Draw(rt, "call")
		current.Store(&c)

		n++
		dir := filepath.Join(base, strconv.Itoa(n))

		require.NoError(rt, m.StartRecording(
			RecordDir(dir),
			RecordTarget(target.URL),
			RecordRequestHeaders("Accept"),
			RecordFormat(rapid.SampledFrom(MockFileFormats()).Draw(rt, "format")),
			RecordMaxInlineBody(rapid.SampledFrom([]int64{0, 16}).Draw(rt, "max inline body")),
			// Every header the target sent, its cookies too.
			RecordExcludeResponseHeaders(),
		))

		header, body := c.wire(rt)
		original, originalBody := exchange(t.Context(), rt, c.method, m.URL()+c.target(), header, body)

		res, err := m.StopRecording(t.Context())
		require.NoError(rt, err)
		require.Len(rt, res.Recordings, 1)

		header, body = c.wire(rt)
		replayed, replayedBody := exchange(t.Context(), rt, c.method, m.URL()+c.target(), header, body)
		requireSameAnswer(rt, original, replayed, originalBody, replayedBody)

		_, err = os.Stat(dir)
		require.NoError(rt, err)
	})
}

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
	const notAbsolute = "expected an absolute http or https URL, such as http://api.local:8080"

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
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))

	require.Empty(t, stopRecording(t, m).Recordings)
}

func TestRecording_ATargetThatBreaksOff(t *testing.T) {
	// An answer the target cut short reaches the client cut short, whether it said its length or not, and isn't
	// recorded.
	for name, answer := range map[string]http.HandlerFunc{
		"with a Content-Length": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(HeaderContentLength, "100")
			_, _ = io.WriteString(w, "only part of it")
		},
		"in chunks": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "only part of it")
			http.NewResponseController(w).Flush()
			panic(http.ErrAbortHandler)
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := newTarget(t, answer)
			dir := t.TempDir()

			m := mockedServer(t, Setup().Silent())
			require.NoError(t, m.StartRecording(RecordDir(dir), RecordTarget(target.URL)))

			res, err := _rawClient.Get(m.URL("/a"))
			if err == nil {
				_, err = io.ReadAll(res.Body)
				_ = res.Body.Close()
			}

			require.Error(t, err, "the client knows the answer broke off")
			require.Empty(t, stopRecording(t, m).Recordings)
			require.Empty(t, recordedMocks(t, dir))
		})
	}
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
	// A recording sends its target the requests it takes, credentials included: it verifies the target's certificate,
	// so a certificate no one signed, as test servers have, fails the call. Skipping the verification records it.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") }))
	t.Cleanup(target.Close)

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

	res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "certificate")
	require.Empty(t, stopRecording(t, m).Recordings)

	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordSkipSSLVerify(true)))

	_, body = do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "secure", string(body))
	require.Len(t, stopRecording(t, m).Recordings, 1)
}

func TestRecording_ATLSTargetThroughItsOwnTransport(t *testing.T) {
	// Skipping the verification of a target with a private certificate also trusts whoever answers in its place. A
	// transport that trusts the target's CA records it with the verification on.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secure") }))
	t.Cleanup(target.Close)

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL), RecordTransport(target.Client().Transport)))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Equal(t, "secure", string(body))
	require.Len(t, stopRecording(t, m).Recordings, 1)
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

func TestRecording_LogsTheAnswersOfItsTarget(t *testing.T) {
	// No mock answers a call its target answers: the request log still says how the call ended, with the answer.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "from the target") })

	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
		m.MustStart()
		t.Cleanup(m.Close)
		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

		do(t, http.MethodGet, m.URL("/a"), nil, nil)
		stopRecording(t, m)

		require.Contains(t, logs.String(), "REQUEST PROXIED GET /a")
		require.Contains(t, logs.String(), `"status":200`)
		require.Contains(t, logs.String(), "from the target")
	})

	t.Run("descriptive", func(t *testing.T) {
		// The descriptive log goes to os.Stdout, which the server takes when it's made.
		r, w, err := os.Pipe()
		require.NoError(t, err)

		stdout := os.Stdout
		os.Stdout = w
		m := MustNew(Setup().UseDescriptiveLogger().Colors(false).LogVerbosity(vlatte.LogBody))
		os.Stdout = stdout

		m.MustStart()
		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

		do(t, http.MethodGet, m.URL("/a"), nil, nil)
		stopRecording(t, m)

		m.Close()
		require.NoError(t, w.Close())

		out, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Contains(t, string(out), "REQUEST PROXIED")
		require.Contains(t, string(out), "Status: 200")
		require.Contains(t, string(out), "from the target")
	})
}

// callModel is a call a recording takes: a request of a kind the server reads, and the answer the target gives.
type callModel struct {
	method string
	path   string
	query  [][2]string // in the order they are sent, a name repeating
	accept string      // "" sends none
	body   bodyModel

	status  int
	header  http.Header
	payload []byte
	gzip    bool
	trailer http.Header
}

// bodyModel is a request body of a kind the server reads.
type bodyModel struct {
	kind        string // "", json, form, multipart, text or binary
	contentType string
	data        []byte
	form        *formModel
}

// target returns the path and query of the request.
func (c callModel) target() string {
	pairs := make([]string, 0, len(c.query))
	for _, p := range c.query {
		pairs = append(pairs, p[0]+"="+p[1])
	}

	if len(pairs) == 0 {
		return c.path
	}

	return c.path + "?" + strings.Join(pairs, "&")
}

// wire returns the request as it is sent. A multipart form gets a fresh boundary each time.
func (c callModel) wire(t require.TestingT) (http.Header, []byte) {
	header := make(http.Header)
	if c.accept != "" {
		header.Set("Accept", c.accept)
	}

	body := c.body.data
	contentType := c.body.contentType

	if c.body.form != nil {
		contentType, body = c.body.form.encode(t)
	}

	if contentType != "" {
		header.Set(HeaderContentType, contentType)
	}

	return header, body
}

// serverRequest returns the request as a server receives it.
func (c callModel) serverRequest(t require.TestingT) *http.Request {
	header, body := c.wire(t)

	r := httptest.NewRequest(c.method, c.target(), bytes.NewReader(body))
	r.Header = header

	return r
}

// mutated returns a request that differs from c in a part a recording matches, which no request the generators draw
// has: '~' is in no token.
func (c callModel) mutated(t *rapid.T) callModel {
	m := c
	m.query = slices.Clone(c.query)

	parts := []string{"path", "accept"}
	if len(c.query) > 0 {
		parts = append(parts, "query")
	}

	// The server reads the form of a POST, PUT or PATCH only, as http.Request.ParseForm does.
	switch c.body.kind {
	case "json", "text":
		parts = append(parts, c.body.kind)
	case "form":
		if c.method == http.MethodPost || c.method == http.MethodPut || c.method == http.MethodPatch {
			parts = append(parts, "form")
		}
	case "multipart":
		parts = append(parts, "field")
	}

	switch rapid.SampledFrom(parts).Draw(t, "mutated part") {
	case "path":
		m.path += "~"
	case "accept":
		m.accept += "~"
	case "query":
		i := rapid.IntRange(0, len(m.query)-1).Draw(t, "mutated parameter")
		m.query[i][1] += "~"
	case "json":
		m.body.data = []byte(`{"~": ` + string(c.body.data) + `}`)
	case "form":
		m.body.data = append(slices.Clone(c.body.data), "&~=1"...)
	case "text":
		m.body.data = append(slices.Clone(c.body.data), '~')
	case "field":
		form := *c.body.form
		form.fields = append(slices.Clone(form.fields), formField{name: "~", value: "1"})
		m.body.form = &form
	}

	return m
}

// answer writes the target's answer to c.
func (c callModel) answer(w http.ResponseWriter) {
	for k, values := range c.header {
		w.Header()[k] = slices.Clone(values)
	}

	for k := range c.trailer {
		w.Header().Add(HeaderTrailer, k)
	}

	body := c.payload
	if c.gzip {
		var buf bytes.Buffer

		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(body)
		_ = gz.Close()

		body = buf.Bytes()
		w.Header().Set(HeaderContentEncoding, "gzip")
	}

	w.WriteHeader(c.status)
	_, _ = w.Write(body)

	for k, values := range c.trailer {
		w.Header()[k] = slices.Clone(values)
	}
}

// exchange sends a request and reads its whole response.
func exchange(ctx context.Context, t require.TestingT, method, target string, header http.Header, body []byte) (*http.Response, []byte) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	require.NoError(t, err)

	req.Header = header

	res, err := _rawClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// matchingMocks returns the IDs of the mocks of m that match req, a request as the server receives it.
func matchingMocks(t require.TestingT, m *Server, req *http.Request) []string {
	rv, _, _ := parseRequest(req, m)
	if req.MultipartForm != nil {
		defer req.MultipartForm.RemoveAll()
	}

	input := &selectorInput{
		RawRequest: req,
		URL:        rv.URL,
		Query:      req.URL.Query(),
		Form:       req.PostForm,
		Files:      formFiles(req),
		ParsedBody: rv.ParsedBodyData,
	}

	mocks, err := m.storage.List(context.Background())
	require.NoError(t, err)

	var ids []string

	for mock := range mocks {
		ok, err := vlatte.Evaluators[*Mock, *selectorInput](mock.spec.expectations).Eval(context.Background(), vlatte.NewExplanation(), input)
		require.NoError(t, err)

		if ok {
			ids = append(ids, mock.ID())
		}
	}

	return ids
}

func TestPropRecording_RoundTrip(t *testing.T) {
	// A recording answers its request as the target did, in every format, with bodies inline or in files, kept in
	// memory or spooled: the same status, headers, trailers and body.
	var current atomic.Pointer[callModel]

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { current.Load().answer(w) })

	rec := MustNew(Setup().Silent())
	rec.MustStart()
	t.Cleanup(rec.Close)
	rec.MustMock(AnyMethod().Reply(From(target.URL)))

	base := t.TempDir()
	replayDir := filepath.Join(base, "replay")
	require.NoError(t, os.Mkdir(replayDir, 0o700))
	replay := replayServer(t, replayDir)

	n := 0

	rapid.Check(t, func(rt *rapid.T) {
		c := genCall().Draw(rt, "call")
		current.Store(&c)

		n++
		dir := filepath.Join(base, strconv.Itoa(n))

		options := []RecordConfigurer{
			RecordDir(dir),
			RecordFormat(rapid.SampledFrom(MockFileFormats()).Draw(rt, "format")),
			RecordBodyFiles(rapid.Bool().Draw(rt, "body files")),
			RecordMaxInlineBody(rapid.SampledFrom([]int64{0, 16}).Draw(rt, "max inline body")),
			RecordRequestHeaders("Accept"),
			// Every header the target sent, its cookies too.
			RecordExcludeResponseHeaders(),
		}

		require.NoError(rt, rec.StartRecording(options...))

		header, body := c.wire(rt)
		original, originalBody := exchange(t.Context(), rt, c.method, rec.URL()+c.target(), header, body)

		res, err := rec.StopRecording(t.Context())
		require.NoError(rt, err)
		require.Len(rt, res.Recordings, 1)

		clearDir(rt, replayDir)

		entries, err := os.ReadDir(dir)
		require.NoError(rt, err)

		for _, e := range entries {
			require.False(rt, strings.HasSuffix(e.Name(), ".tmp"), "a temporary file is left: %s", e.Name())

			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(rt, err)
			require.NoError(rt, os.WriteFile(filepath.Join(replayDir, e.Name()), b, 0o600))
		}

		require.NoError(rt, replay.Reload(), "the recording loads")

		header, body = c.wire(rt)
		replayed, replayedBody := exchange(t.Context(), rt, c.method, replay.URL()+c.target(), header, body)
		requireSameAnswer(rt, original, replayed, originalBody, replayedBody)
	})
}

func TestPropRecording_OneRecordingPerRequest(t *testing.T) {
	// Each request a recording took matches one recorded mock, and a request that differs in a part a recording
	// matches matches none: a recording never answers a request it didn't record.
	rec := MustNew(Setup().Silent())
	rec.MustStart()
	t.Cleanup(rec.Close)
	rec.MustMock(AnyMethod().Reply(OK().PlainText("ok")))

	base := t.TempDir()
	n := 0

	rapid.Check(t, func(rt *rapid.T) {
		calls := rapid.SliceOfN(genCall(), 1, 5).Draw(rt, "calls")

		n++
		dir := filepath.Join(base, strconv.Itoa(n))
		require.NoError(rt, rec.StartRecording(RecordDir(dir), RecordRequestHeaders("Accept"), RecordIncludeMocked(true)))

		for _, c := range calls {
			header, body := c.wire(rt)
			res, _ := exchange(t.Context(), rt, c.method, rec.URL()+c.target(), header, body)
			require.Equal(rt, http.StatusOK, res.StatusCode)
		}

		_, err := rec.StopRecording(t.Context())
		require.NoError(rt, err)

		replay := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.json")))
		require.NoError(rt, replay.Reload())

		for i, c := range calls {
			require.Len(rt, matchingMocks(rt, replay, c.serverRequest(rt)), 1, "call %d: %s %s", i, c.method, c.target())

			other := c.mutated(rt)
			require.Empty(rt, matchingMocks(rt, replay, other.serverRequest(rt)), "call %d changed: %s %s", i, other.method, other.target())
		}
	})
}

// discardWriter answers a request into nothing, so a benchmark measures the server alone.
type discardWriter struct {
	header http.Header
}

func (w *discardWriter) Header() http.Header         { return w.header }
func (w *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *discardWriter) WriteHeader(int)             {}

// benchHandler returns the handler of a server, which it serves without listening.
func benchHandler(b *testing.B, mock *Builder) (*Server, http.Handler) {
	b.Helper()

	var handler http.Handler

	m := MustNew(Setup().Silent().HandlerDecorator(func(h http.Handler) http.Handler {
		handler = h
		return h
	}))
	b.Cleanup(m.Close)
	m.MustMock(mock)

	return m, handler
}

func BenchmarkServeHTTP_Recording(b *testing.B) {
	// What a recording adds to a request: a small answer, recorded or not.
	for _, recording := range []bool{false, true} {
		b.Run(fmt.Sprintf("recording=%v", recording), func(b *testing.B) {
			m, handler := benchHandler(b, Getf("/items").Reply(OK().PlainText("a small answer")))
			if recording {
				require.NoError(b, m.StartRecording(RecordDir(b.TempDir()), RecordIncludeMocked(true)))
			}

			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(&discardWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/items?page=2", nil))
			}
		})
	}
}

func BenchmarkRecording_LargeBody(b *testing.B) {
	// A large body goes to a file as it is captured: the memory a call takes doesn't grow with its body.
	for _, size := range []int{1 << 20, 8 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			body := bytes.Repeat([]byte("0123456789abcdef"), size/16)

			m, handler := benchHandler(b, Getf("/big").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
				// As a proxy copies a target's body.
				_, _ = io.CopyBuffer(w, struct{ io.Reader }{bytes.NewReader(body)}, make([]byte, 32<<10))
			})))

			require.NoError(b, m.StartRecording(RecordDir(b.TempDir()), RecordIncludeMocked(true)))

			b.SetBytes(int64(size))
			b.ReportAllocs()

			for b.Loop() {
				handler.ServeHTTP(&discardWriter{header: make(http.Header)}, httptest.NewRequest(http.MethodGet, "/big", nil))
			}

			b.StopTimer()

			// A body the disk falls behind isn't recorded: the call fails, and the answer goes on.
			res, _ := m.StopRecording(b.Context())
			require.NotNil(b, res)
			b.ReportMetric(float64(res.Status.Failed)/float64(res.Status.Calls), "failed/op")
		})
	}
}

// Generators

func genCall() *rapid.Generator[callModel] {
	return rapid.Custom(func(t *rapid.T) callModel {
		c := callModel{
			method: rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:   "/" + strings.Join(rapid.SliceOfN(genToken(), 1, 3).Draw(t, "path"), "/"),
			status: rapid.SampledFrom(_genStatuses).Draw(t, "status"),
			header: make(http.Header),
		}

		names := rapid.SliceOfN(rapid.StringMatching(`[a-c]`), 0, 4).Draw(t, "query names")
		for _, name := range names {
			c.query = append(c.query, [2]string{name, genToken().Draw(t, "query value")})
		}

		if rapid.Bool().Draw(t, "accept") {
			c.accept = genToken().Draw(t, "accept value")
		}

		if c.method != http.MethodGet {
			c.body = genBody().Draw(t, "body")
		}

		c.header.Set(HeaderContentType, rapid.SampledFrom([]string{MIMETextPlain, MIMEApplicationJSON, "image/png", ""}).Draw(t, "content type"))
		if c.header.Get(HeaderContentType) == "" {
			c.header.Del(HeaderContentType)
		}

		for range rapid.IntRange(0, 2).Draw(t, "cookies") {
			c.header.Add("Set-Cookie", genToken().Draw(t, "cookie")+"=1")
		}

		switch rapid.IntRange(0, 2).Draw(t, "payload") {
		case 1:
			c.payload = []byte(rapid.String().Draw(t, "text"))
		case 2:
			c.payload = rapid.SliceOfN(rapid.Byte(), 1, 64).Draw(t, "bytes")
		}

		c.gzip = len(c.payload) > 0 && rapid.Bool().Draw(t, "gzip")

		if rapid.Bool().Draw(t, "trailers") {
			c.trailer = http.Header{"X-Sum": rapid.SliceOfN(genToken(), 1, 2).Draw(t, "trailer")}
		}

		return c
	})
}

func genBody() *rapid.Generator[bodyModel] {
	return rapid.Custom(func(t *rapid.T) bodyModel {
		switch kind := rapid.SampledFrom([]string{"", "json", "form", "multipart", "text", "binary"}).Draw(t, "kind"); kind {
		case "json":
			doc := make(map[string]any)
			for _, k := range rapid.SliceOfN(genToken(), 0, 3).Draw(t, "names") {
				doc[k] = rapid.OneOf(
					rapid.Map(genToken(), func(s string) any { return s }),
					rapid.Map(rapid.Int64(), func(n int64) any { return n }),
					rapid.Map(rapid.SliceOfN(rapid.IntRange(0, 9), 0, 3), func(l []int) any { return l }),
				).Draw(t, "value")
			}

			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("json: %v", err)
			}

			// JSON written another way is the same request.
			if rapid.Bool().Draw(t, "indented") {
				v := jsontext.Value(data)
				if err := v.Indent(); err != nil {
					t.Fatalf("json: %v", err)
				}

				data = v
			}

			return bodyModel{kind: kind, contentType: "application/json; charset=utf-8", data: data}
		case "form":
			var pairs []string
			for _, name := range rapid.SliceOfN(rapid.StringMatching(`[a-c]`), 0, 4).Draw(t, "fields") {
				pairs = append(pairs, name+"="+genToken().Draw(t, "field value"))
			}

			return bodyModel{kind: kind, contentType: MIMEFormURLEncoded, data: []byte(strings.Join(pairs, "&"))}
		case "multipart":
			form := genForm().Draw(t, "form")
			return bodyModel{kind: kind, form: &form}
		case "text":
			return bodyModel{kind: kind, contentType: MIMETextPlain, data: []byte(genToken().Draw(t, "text"))}
		case "binary":
			return bodyModel{kind: kind, contentType: "application/octet-stream", data: []byte{0xff, rapid.Byte().Draw(t, "byte")}}
		default:
			return bodyModel{}
		}
	})
}
