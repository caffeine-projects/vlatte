package mockhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
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

	// The reply's headers reach the client through the steps too.
	m.MustMock(Getf("/file").Step(appendStep("")).Reply(OK().Header("X-Id", "7").BodyFile(file)))

	res, err := http.Get(m.URL("/file"))
	require.NoError(t, err)

	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "7", res.Header.Get("X-Id"))
	require.Equal(t, body, got)
}

// failingStep is a step that fails every write.
type failingStep struct{}

func (failingStep) Handle(context.Context, []byte, vlatte.Next) (int, error) {
	return 0, errors.New("step: failed")
}

func TestTopResponseWriter_AFailingStepFailsTheWrite(t *testing.T) {
	// A write a step fails writes nothing, and says why: io.Copy and Render stop there.
	rec := httptest.NewRecorder()
	w := &topResponseWriter{ctx: t.Context(), steps: []vlatte.Step{failingStep{}}, rw: rec}

	n, err := w.Write([]byte("a"))
	require.EqualError(t, err, "step: failed")
	require.Zero(t, n)
	require.Empty(t, rec.Body.String())
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

func TestHandler_FailuresAreToldApartFromNoMatch(t *testing.T) {
	// Issue #17: a mock that matched and failed answered as if no mock matched, with a body blaming matching.
	// Both keep the 418, so neither looks like a mock's answer, and X-Vlatte-Error says which one it was.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").ID("file-mock").Name("missing file").SetSource("mocks/x.yaml").
		Reply(OK().BodyFile("missing.txt")))

	status, header, body := getFull(t, m, "/x")
	require.Equal(t, StatusNoMatch, status, body)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderXVlatteError))
	require.True(t, strings.HasPrefix(body, "REPLY FAILED\nGET "+m.URL("/x")+"\n"), body)
	require.Contains(t, body, "\nMock [file-mock] missing file\nSource: mocks/x.yaml\n", "the body names the mock")
	require.Contains(t, body, "missing.txt", "and the error")

	status, header, body = getFull(t, m, "/nothing")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderXVlatteError))
	require.True(t, strings.HasPrefix(body, "REQUEST DID NOT MATCH\n"), body)
}

func TestHandler_ErrorStatusCodeIsItsOwnSetting(t *testing.T) {
	// A user who wants failures apart from no-match by status alone sets the error status; no-match keeps its own.
	m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Reply(OK().BodyFile("missing.txt")))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusInternalServerError, status)

	status, _ = get(t, m, "/nothing")
	require.Equal(t, StatusNoMatch, status)
}

func TestHandler_FailuresSkipTheStepsOfTheMock(t *testing.T) {
	// The steps of a mock change its answer. A failure is vlatte's answer, so the steps must not change it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Step(appendStep("<step>")).Reply(OK().BodyFile("missing.txt")))
	m.MustMock(Getf("/mapper").Step(appendStep("<step>")).Reply(OK()).
		Map(func(*RequestValues, *Response) error { return errors.New("mapper failed") }))

	for _, path := range []string{"/x", "/mapper"} {
		status, header, body := getFull(t, m, path)
		require.Equal(t, StatusNoMatch, status, body)
		require.Equal(t, vlatte.FailureReply, header.Get(HeaderXVlatteError))
		require.NotContains(t, body, "<step>")
	}
}

// failingStore is a store whose List fails.
type failingStore struct {
	*vlatte.MemoryStore[*Mock]
}

func (failingStore) List(context.Context) (iter.Seq[*Mock], error) {
	return nil, errors.New("store down")
}

func TestHandler_StoreAndMatcherErrorsAreFailures(t *testing.T) {
	// A store that failed answered an empty 200, and a matcher that failed answered as if no mock matched.
	// Both are failures to find a mock, so a test can't mistake them for a mock's answer or for a missing mock.
	t.Run("store", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.storage = failingStore{vlatte.NewStore[*Mock]().(*vlatte.MemoryStore[*Mock])}
		m.MustStart()
		t.Cleanup(m.Close)

		status, header, body := getFull(t, m, "/x")
		require.Equal(t, StatusNoMatch, status)
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderXVlatteError))
		require.True(t, strings.HasPrefix(body, "MATCHING FAILED\n"), body)
		require.Contains(t, body, "store down")
	})

	t.Run("matcher", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		scoped := m.MustMock(Getf("/x").ID("broken").Header("test", match.Function(
			func(any) (bool, error) { return false, errors.New("matcher failed") })).Reply(OK()))

		status, header, body := getFull(t, m, "/x")
		require.Equal(t, StatusNoMatch, status)
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderXVlatteError))
		require.Contains(t, body, "Mock [broken]", "the error names the mock whose matcher failed")
		require.Contains(t, body, "matcher failed")
		require.False(t, scoped.Called())
	})
}

func TestHandler_APanicIsAFailure(t *testing.T) {
	// A panicking reply answers the error status with the panic failure, and the server keeps serving.
	m := MustNew(Setup().Silent().ErrorStatusCode(http.StatusInternalServerError))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/panic").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
		panic("boom")
	})))
	m.MustMock(Getf("/ok").Reply(OK()))

	status, header, body := getFull(t, m, "/panic")
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, vlatte.FailurePanic, header.Get(HeaderXVlatteError))
	require.Contains(t, body, "boom")

	status, _ = get(t, m, "/ok")
	require.Equal(t, http.StatusOK, status)
}

func TestLifecycle_ErrorLogsNameTheMock(t *testing.T) {
	// In a server with many mocks, the log is how a failure is traced to its mock.
	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger))
		m.MustStart()
		t.Cleanup(m.Close)

		m.MustMock(Getf("/x").ID("file-mock").Name("missing file").Reply(OK().BodyFile("missing.txt")))

		get(t, m, "/x")
		require.Contains(t, logs.String(), `"mock":{"id":"file-mock","name":"missing file"}`)
	})

	t.Run("descriptive", func(t *testing.T) {
		// New writes the descriptive log to stdout, so the test builds it with a buffer.
		m := MustNew()
		var out bytes.Buffer
		lifecycle := newBuiltInDescriptiveMockHTTPLifecycle(m, ansi(false), &out)

		mock, err := Getf("/x").ID("a").Name("users").Reply(OK()).Build(m)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		lifecycle.OnError(&RequestValues{RawRequest: req, URL: req.URL, Mock: mock}, errors.New("boom"))
		require.Contains(t, out.String(), "Mock: a users\n")
		require.Contains(t, out.String(), "Error: boom")
	})
}

func TestHandler_TheResponseWaitsForTheEvents(t *testing.T) {
	// A test checks what the events of a mock did, such as a webhook's call, right after the response, with no wait of
	// its own: the response waits for them, global ones too.
	var done atomic.Int32
	slow := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
		time.Sleep(50 * time.Millisecond)
		done.Add(1)

		return nil
	})

	m := MustNew(Setup().Silent().
		OnCompleteListener("slow", slow).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "slow"}}}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "slow"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, int32(2), done.Load(), "the response came once every event was done")
}

func TestHandler_CloseCancelsTheEventsRunning(t *testing.T) {
	// Close stops the events still running, such as a webhook waiting on a slow target, instead of waiting for them:
	// the requests waiting for them end, and the server closes at once. A restarted server runs events again.
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan error, 1)
	var counted atomic.Int32

	m := MustNew(Setup().Silent().
		OnCompleteListener("waits", vlatte.EventFunc[vlatte.OnComplete](func(ctx context.Context, _ vlatte.OnComplete) error {
			close(started)

			select {
			case <-ctx.Done():
			case <-release: // the test failed first
			}

			canceled <- ctx.Err()

			return ctx.Err()
		})).
		OnCompleteListener("counts", vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error {
			counted.Add(1)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	// Last in, first out: the event ends before the server closes, when the test fails first.
	t.Cleanup(func() { close(release) })

	m.MustMock(
		Getf("/waits").OnCompleteEvent(vlatte.EventConfig{Name: "waits"}).Reply(NoContent()),
		Getf("/counts").OnCompleteEvent(vlatte.EventConfig{Name: "counts"}).Reply(NoContent()),
	)

	// The client waits for the event, in a goroutine of its own. Close closes its connection, so it gets no response.
	ended := make(chan struct{})
	go func() {
		defer close(ended)

		if res, err := http.Get(m.URL("/waits")); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the event never started")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		m.Close()
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the event instead of canceling it")
	}

	select {
	case err := <-canceled:
		require.ErrorIs(t, err, context.Canceled)
	default:
		t.Fatal("Close returned before the event it canceled")
	}

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the request waiting for the event never ended")
	}

	m.MustStart()

	status, _ := get(t, m, "/counts")
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, int32(1), counted.Load(), "a restarted server runs events again")
}

func TestHandler_EventsRunWhenTheRequestLogPanics(t *testing.T) {
	// Handler replies return no response, and the request log reads it (#24): it panics, after the reply. The events
	// run anyway, before the response ends. Silent() would skip the request log.
	var ran atomic.Bool
	logger := zerolog.New(io.Discard)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("hook", vlatte.EventFunc[vlatte.OnComplete](
		func(context.Context, vlatte.OnComplete) error {
			ran.Store(true)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").
		OnCompleteEvent(vlatte.EventConfig{Name: "hook"}).
		Reply(Handler(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusOK, status)
	require.True(t, ran.Load(), "the event ran before the response ended")
}

// cancelableKV is a parameter store that fails once its context is done, as a remote store does.
type cancelableKV struct {
	vlatte.KV
}

func (kv cancelableKV) Get(ctx context.Context, k string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return kv.KV.Get(ctx, k)
}

func TestHandler_EventsOutliveTheClient(t *testing.T) {
	// A client that leaves, as one that times out does, ends the context of its request, not the events of the
	// request: they run with a context of their own, and their templates read the parameters with it, through a store
	// that fails once its context is done.
	kv := cancelableKV{vlatte.NewMemoryKV()}
	require.NoError(t, kv.Set(t.Context(), "target", []byte("api")))

	started := make(chan struct{})
	rendered := make(chan string, 1)

	m := MustNew(Setup().Silent().Parameters(kv).OnCompleteListener("renders", vlatte.EventFunc[vlatte.OnComplete](
		func(ctx context.Context, e vlatte.OnComplete) error {
			close(started)

			select {
			case <-e.(*OnComplete).Request().RawRequest.Context().Done():
			case <-ctx.Done(): // Close, when the test fails first
				return ctx.Err()
			}

			var b strings.Builder

			tmpl, err := e.Kit().TemplateEngine().Compile("target", `{{ .Parameters.Get "target" }}`)
			if err == nil {
				err = tmpl.Execute(&b, e.Kit().Data())
			}

			if err == nil {
				err = ctx.Err()
			}

			if err != nil {
				b.WriteString(err.Error())
			}

			rendered <- b.String()

			return err
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "renders"}).Reply(NoContent()))

	ctx, leave := context.WithCancel(t.Context())
	defer leave()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/x"), nil)
	require.NoError(t, err)

	ended := make(chan struct{})
	go func() {
		defer close(ended)

		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the event never started")
	}

	leave()
	<-ended

	select {
	case got := <-rendered:
		require.Equal(t, "api", got)
	case <-time.After(5 * time.Second):
		t.Fatal("the event never saw the client leave")
	}
}
