package mockhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

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
