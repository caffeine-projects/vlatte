package mockhttp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// appendStep appends its text to every buffer it passes on.
type appendStep string

func (s appendStep) Handle(_ context.Context, p []byte, next vlatte.Next) (int, error) {
	return next.Next(append(slices.Clip(p), s...))
}

func TestTopResponseWriter_EveryWritePassesTheSteps(t *testing.T) {
	// D1: a pipeline passes one buffer once, and the writer reused one for every write, so the 2nd write returned
	// io.EOF. Write reports len(p) as well: io.Copy rejects a count larger than the buffer it handed over.
	rec := httptest.NewRecorder()
	w := &topResponseWriter{ctx: t.Context(), steps: []vlatte.Step{appendStep("!")}, rw: rec}

	for _, s := range []string{"a", "b", "c"} {
		n, err := w.Write([]byte(s))
		require.NoError(t, err)
		require.Equal(t, len(s), n)
	}

	require.Equal(t, "a!b!c!", rec.Body.String())
}

func TestTopResponseWriter_FlushReachesTheResponseWriter(t *testing.T) {
	// D2: streamed parts are flushed with http.ResponseController, which gets past the steps through Unwrap.
	rec := httptest.NewRecorder()
	w := &topResponseWriter{ctx: t.Context(), steps: []vlatte.Step{appendStep("")}, rw: rec}

	require.NoError(t, http.NewResponseController(w).Flush())
	require.True(t, rec.Flushed)
}

func TestSteps_ServeABodyFileLargerThanOneWrite(t *testing.T) {
	// D1: io.Copy writes a body file in 32 KB chunks, and through steps every chunk after the first failed.
	body := bytes.Repeat([]byte("0123456789abcdef"), 8<<10)
	file := filepath.Join(t.TempDir(), "body.bin")
	require.NoError(t, os.WriteFile(file, body, 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/file").Step(appendStep("")).Reply(OK().BodyFile(file)))

	res, err := http.Get(m.URL("/file"))
	require.NoError(t, err)

	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, body, got)
}

func TestSteps_ConcurrentRequestsKeepTheirOwnResponse(t *testing.T) {
	// D1: three Step calls leave spare capacity in the mock's steps, and every request appended its sink there, so
	// concurrent requests could write into each other's responses. The race detector also flags it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/echo").
		Step(appendStep("1")).
		Step(appendStep("2")).
		Step(appendStep("3")).
		Reply(OK().BodyTemplate(`{{ .Request.URL.Query.Get "n" }}`)))

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			n := strconv.Itoa(i)

			res, err := http.Get(m.URL("/echo?n=" + n))
			if !assert.NoError(t, err) {
				return
			}

			b, err := io.ReadAll(res.Body)
			assert.NoError(t, err)
			assert.NoError(t, res.Body.Close())
			assert.Equal(t, n+"123", string(b))
		})
	}

	wg.Wait()
}

func TestHandler_RenderErrorsAreLoggedNotWritten(t *testing.T) {
	// D7: a body that fails while it is written already sent its status and headers. onError wrote a second status
	// and appended its error text to that body; now the error is logged and the mock counts as served.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	// A directory opens, then fails to read.
	scoped := m.MustMock(Getf("/dir").Reply(OK().BodyFile(t.TempDir())))

	res, err := http.Get(m.URL("/dir"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, b, "no error text in a body that already started")
	require.True(t, scoped.Called())
	require.Contains(t, logs.String(), "http: rendering response: ")
}

func TestHandler_AClientLeavingAStreamIsAWarning(t *testing.T) {
	// Decision 14: closing an x-mixed-replace stream is how a client stops it. The stream ends with a warning, not
	// an error, and the mock counts as served.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Getf("/stream").Reply(OK().Multipart(NewMultipart("x-mixed-replace",
		NewPart().BodyText("first"),
		NewPart().BodyText("never sent").Delay(time.Hour),
	).Boundary("b"))))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/stream"), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	// The stream sends the first part and the delimiter line of the second, then waits.
	first := make([]byte, len("--b\r\n\r\nfirst\r\n--b\r\n"))
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err)

	cancel()
	_ = res.Body.Close()

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), `"level":"warn","error":"http: rendering response: context canceled"`)
	}, 5*time.Second, 10*time.Millisecond, "logs: %s", logs)
	require.NotContains(t, logs.String(), `"level":"error"`)
	require.True(t, scoped.Called())
}
