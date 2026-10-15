package mockhttp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestRecording_CapturesTheAnswerTheClientGets(t *testing.T) {
	// The capture sees what the server sends, the way net/http sends it: the status of the first final WriteHeader,
	// or 200, and the body after the steps of the mock.
	tests := []struct {
		name   string
		mock   *Builder
		status int
		body   string
	}{
		{
			name:   "a body without a status is 200",
			mock:   Getf("/r").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "body") })),
			status: http.StatusOK,
			body:   "body",
		},
		{
			name: "a second status is ignored",
			mock: Getf("/r").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.WriteHeader(http.StatusInternalServerError)
			})),
			status: http.StatusCreated,
		},
		{
			name: "an informational status isn't the answer",
			mock: Getf("/r").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Link", "</style.css>; rel=preload")
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusAccepted)
			})),
			status: http.StatusAccepted,
		},
		{
			name:   "nothing written is 200",
			mock:   Getf("/r").Reply(Handler(func(http.ResponseWriter, *http.Request) {})),
			status: http.StatusOK,
		},
		{
			name:   "the steps of a mock change what is sent",
			mock:   Getf("/r").Step(appendStep("<step>")).Reply(OK().PlainText("body")),
			status: http.StatusOK,
			body:   "body<step>",
		},
		{
			name: "a flush sends the status",
			mock: Getf("/r").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
				http.NewResponseController(w).Flush()
				w.WriteHeader(http.StatusTeapot)
				_, _ = io.WriteString(w, "body")
			})),
			status: http.StatusOK,
			body:   "body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(tc.mock)
			require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

			res, body := do(t, http.MethodGet, m.URL("/r"), nil, nil)
			require.Equal(t, tc.status, res.StatusCode)
			require.Equal(t, tc.body, string(body))

			stopRecording(t, m)

			mock := onlyMock(t, dir)
			require.Equal(t, tc.status, mock.Response.Status)
			require.Equal(t, tc.body, mock.Response.Body)
		})
	}
}

func TestRecording_LeavesOutTheHeadersSetBeforeTheHandler(t *testing.T) {
	// CORS and other middlewares set headers on every answer: they aren't the answer's, unless the reply sets them.
	dir := t.TempDir()

	addVary := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Vary", "Origin")
			w.Header().Set("X-Middleware", "set")
			w.Header().Set("X-Replaced", "set")
			next.ServeHTTP(w, r)
		})
	}

	m := MustNew(Setup().Silent().CORS().Middlewares(addVary))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/r").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Accept")
		w.Header().Set("X-Replaced", "by the reply")
		w.Header().Set("X-Reply", "1")
	})))

	require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true)))

	res, _ := do(t, http.MethodGet, m.URL("/r"), http.Header{"Origin": {"https://example.org"}}, nil)
	require.NotEmpty(t, res.Header.Get("Access-Control-Allow-Origin"), "CORS answered")

	stopRecording(t, m)

	require.Equal(t, map[string][]string{
		"Vary":       {"Accept"},
		"X-Replaced": {"by the reply"},
		"X-Reply":    {"1"},
	}, onlyMock(t, dir).Response.Header)
}

func TestRecording_Trailers(t *testing.T) {
	// The trailers a client gets are recorded: those announced, and those named with http.TrailerPrefix, with every
	// value. A target's trailers come through a proxy reply the same way.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderTrailer, "X-Sum")
		_, _ = io.WriteString(w, "body")
		w.Header().Add("X-Sum", "1")
		w.Header().Add("X-Sum", "2")
		w.Header().Set(http.TrailerPrefix+"X-Late", "3")
	})

	m, dir := recordingServer(t, target.URL)

	original, originalBody := do(t, http.MethodGet, m.URL("/r"), nil, nil)
	require.Equal(t, http.Header{"X-Sum": {"1", "2"}, "X-Late": {"3"}}, original.Trailer)

	stopRecording(t, m)

	mock := onlyMock(t, dir)
	require.Equal(t, map[string][]string{"X-Sum": {"1", "2"}, "X-Late": {"3"}}, mock.Response.Trailer)
	require.NotContains(t, mock.Response.Header, HeaderTrailer, "the announcement isn't a header")

	replayed, replayedBody := do(t, http.MethodGet, replayServer(t, dir).URL("/r"), nil, nil)
	require.Equal(t, original.Trailer, replayed.Trailer)
	require.Equal(t, originalBody, replayedBody)
}

func TestRecording_Repeats(t *testing.T) {
	// A request that comes again plays back its answers in turn, then its last one again: unless it is the same
	// answer every time, which is one. RecordFirst keeps the first answer only.
	answers := []string{"a", "a", "b", "a"}

	tests := []struct {
		name     string
		repeats  vlatte.RecordRepeats
		calls    int
		answers  []string
		playback []string
	}{
		{"the same answer is one", vlatte.RecordSequence, 2, []string{"a"}, []string{"a", "a"}},
		{"answers in turn", vlatte.RecordSequence, 4, []string{"a", "a", "b", "a"}, []string{"a", "a", "b", "a", "a"}},
		{"the first answer", vlatte.RecordFirst, 4, []string{"a"}, []string{"a", "a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var next atomic.Int64

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/r").Reply(Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
				_, err := io.WriteString(w, answers[next.Add(1)-1])
				return nil, err
			})))

			dir := t.TempDir()
			require.NoError(t, m.StartRecording(RecordDir(dir), RecordIncludeMocked(true), RecordRepeats(tc.repeats)))

			for range tc.calls {
				do(t, http.MethodGet, m.URL("/r"), nil, nil)
			}

			res := stopRecording(t, m)
			require.Len(t, res.Recordings, 1)
			require.Equal(t, len(tc.answers), res.Recordings[0].Responses)

			mock := onlyMock(t, dir)

			var recorded []string
			if mock.Response != nil {
				recorded = append(recorded, mock.Response.Body)
			} else {
				for _, r := range mock.Sequence.Responses {
					recorded = append(recorded, r.Body)
				}

				require.Equal(t, recorded[len(recorded)-1], mock.Sequence.AfterEnded.Body, "the last answer repeats")
			}

			require.Equal(t, tc.answers, recorded)

			replay := replayServer(t, dir)

			var played []string
			for range tc.playback {
				_, body := do(t, http.MethodGet, replay.URL("/r"), nil, nil)
				played = append(played, string(body))
			}

			require.Equal(t, tc.playback, played)
		})
	}
}

func TestRecording_TransformsAreTyped(t *testing.T) {
	// A transform changes a recording as Go values, before it is written: here, it removes the cookies of every answer
	// and matches any value of a query parameter.
	target := newTarget(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret"})
		_, _ = io.WriteString(w, "page "+r.URL.Query().Get("page"))
	})

	m, dir := recordingServer(t, target.URL, RecordTransform(func(r *RecordedMock) error {
		r.Name = "items"
		r.ID = "ignored"
		r.Request.URL = strings.Split(r.Request.URL, "?")[0]

		for i := range r.Responses {
			delete(r.Responses[i].Header, "Set-Cookie")
		}

		return nil
	}))

	do(t, http.MethodGet, m.URL("/items?page=1"), nil, nil)

	res := stopRecording(t, m)
	require.Len(t, res.Recordings, 1)
	require.True(t, strings.HasPrefix(res.Recordings[0].ID, "GET-items-page-1-"), "a transform can't change the ID")

	mock := onlyMock(t, dir)
	require.Equal(t, res.Recordings[0].ID, mock.ID)
	require.Equal(t, "items", mock.Name)
	require.Equal(t, "/items", mock.Request.URL)
	require.NotContains(t, mock.Response.Header, "Set-Cookie")

	_, body := do(t, http.MethodGet, replayServer(t, dir).URL("/items"), nil, nil)
	require.Equal(t, "page 1", string(body))
}

func TestRecording_LargeBodies(t *testing.T) {
	// A large body streams to the client as the target sends it, goes to a file as it is captured, and plays back
	// byte for byte, whatever its size: the server never holds it whole.
	const size = 8 << 20

	body := bytes.Repeat([]byte("0123456789abcdef"), size/16)

	rest := make(chan struct{})
	t.Cleanup(closeOnce(rest))

	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, "application/zip")
		w.Header().Set(HeaderContentLength, "8388608")

		_, _ = w.Write(body[:size/2])
		<-rest
		_, _ = w.Write(body[size/2:])
	})

	m, dir := recordingServer(t, target.URL, RecordMaxInlineBody(64<<10))

	res, err := http.Get(m.URL("/exports/2026.zip"))
	require.NoError(t, err)

	first := make([]byte, size/4)
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err, "the client reads while the target still holds the rest")

	close(rest)

	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, body[:size/4], first)
	require.Equal(t, body[size/4:], got)

	recorded := stopRecording(t, m)
	require.Len(t, recorded.Recordings, 1)

	mock := onlyMock(t, dir)
	sum := sha256.Sum256(body)
	require.Equal(t, "GET-exports-2026.zip-"+hex.EncodeToString(sum[:])[:16]+".zip", mock.Response.BodyFile)
	require.Equal(t, []string{mock.Response.BodyFile}, recorded.Recordings[0].BodyFiles)

	file, err := os.ReadFile(filepath.Join(dir, mock.Response.BodyFile))
	require.NoError(t, err)
	require.Equal(t, body, file)

	_, replayed := do(t, http.MethodGet, replayServer(t, dir).URL("/exports/2026.zip"), nil, nil)
	require.Equal(t, body, replayed)

	t.Run("a body larger than MaxBodySize isn't recorded, and still reaches the client", func(t *testing.T) {
		target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body[:1<<20]) })

		m, dir := recordingServer(t, target.URL, RecordMaxInlineBody(1<<10), RecordMaxBodySize(64<<10))

		_, got := do(t, http.MethodGet, m.URL("/big"), nil, nil)
		require.Equal(t, body[:1<<20], got)

		res, err := m.StopRecording(t.Context())
		require.Error(t, err)
		require.Empty(t, res.Recordings)
		require.Equal(t, int64(1), res.Status.Failed)
		require.Empty(t, recordedMocks(t, dir))
	})
}

func TestCaptureWriter_KeepsOnlyAnswersItRecords(t *testing.T) {
	// A reply such as Echo writes before the server knows who answers: the capture keeps nothing until it knows the
	// answer is recorded, so an answer it doesn't record costs it neither memory nor the disk.
	for _, includeMocked := range []bool{false, true} {
		m := MustNew(Setup().Silent())
		t.Cleanup(m.Close)
		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordIncludeMocked(includeMocked)))

		capture := func(method string) *captureWriter {
			r := httptest.NewRequest(method, "/r", nil)
			rv, raw, err := parseRequest(r, m)

			return m.recording.Load().capture(httptest.NewRecorder(), rv, raw, err)
		}

		c := capture(http.MethodGet)
		_, _ = c.Write([]byte("written by the reply"))
		require.Equal(t, includeMocked, c.call.resBody.Len() > 0, "IncludeMocked %v", includeMocked)
		c.end()

		c = capture(http.MethodGet)
		c.answeredBy(true)
		_, _ = c.Write([]byte("a proxy's answer"))
		require.Positive(t, c.call.resBody.Len(), "a proxied answer is kept")
		c.end()

		c = capture(http.MethodHead)
		c.answeredBy(true)
		_, _ = c.Write([]byte("no body"))
		require.Zero(t, c.call.resBody.Len(), "a HEAD answer has no body")
		c.end()
	}
}
