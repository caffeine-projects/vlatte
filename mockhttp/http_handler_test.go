package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	// Both keep the 418, so neither looks like a mock's answer, and VLatte-Error says which one it was.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").ID("file-mock").Name("missing file").SetSource("mocks/x.yaml").
		Reply(OK().BodyFile("missing.txt")))

	status, header, body := getFull(t, m, "/x")
	require.Equal(t, StatusNoMatch, status, body)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.True(t, strings.HasPrefix(body, "REPLY FAILED\nGET "+m.URL("/x")+"\n"), body)
	require.Contains(t, body, "\nMock [file-mock] missing file\nSource: mocks/x.yaml\n", "the body names the mock")
	require.Contains(t, body, "missing.txt", "and the error")

	status, header, body = getFull(t, m, "/nothing")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
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
		require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
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
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderVlatteError))
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
		require.Equal(t, vlatte.FailureMatch, header.Get(HeaderVlatteError))
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
	require.Equal(t, vlatte.FailurePanic, header.Get(HeaderVlatteError))
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
		lifecycle.OnError(&RequestValues{ReqID: "r1", RawRequest: req, URL: req.URL, Mock: mock}, errors.New("boom"))
		require.Contains(t, out.String(), "Request ID: r1\n", "the ID a client reads in the VLatte-Request-Id header")
		require.Contains(t, out.String(), "Mock: a users\n")
		require.Contains(t, out.String(), "Error: boom")
	})
}

func TestLifecycle_TheRequestLogTimesTheEvents(t *testing.T) {
	// The request log was written before the events ran, so its elapsed time left them out, though the response waits
	// for them: a webhook that took a second logged a request of a millisecond.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("slow", vlatte.EventFunc[vlatte.OnComplete](
		func(context.Context, vlatte.OnComplete) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "slow"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)

	var matched []float64
	for line := range strings.Lines(logs.String()) {
		var entry struct {
			Message string  `json:"message"`
			Elapsed float64 `json:"elapsed"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry), line)

		if strings.HasPrefix(entry.Message, "<--- REQUEST MATCHED") {
			matched = append(matched, entry.Elapsed)
		}
	}

	require.Len(t, matched, 1)
	require.GreaterOrEqual(t, matched[0], 100.0, "the elapsed time, in milliseconds, counts the events")
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
	// the requests waiting for them end, and the server closes at once.
	started := make(chan struct{})
	release := make(chan struct{})
	canceled := make(chan error, 1)

	m := MustNew(Setup().Silent().
		OnCompleteListener("waits", vlatte.EventFunc[vlatte.OnComplete](func(ctx context.Context, _ vlatte.OnComplete) error {
			close(started)

			select {
			case <-ctx.Done():
			case <-release: // the test failed first
			}

			canceled <- ctx.Err()

			return ctx.Err()
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	// Last in, first out: the event ends before the server closes, when the test fails first.
	t.Cleanup(func() { close(release) })

	m.MustMock(Getf("/waits").OnCompleteEvent(vlatte.EventConfig{Name: "waits"}).Reply(NoContent()))

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
}

func TestHandler_EventsRunForRepliesWithoutAResponse(t *testing.T) {
	// Handler replies return no response. The events of their mock run all the same, and the request log, which read
	// the response and panicked (#24), logs the match. Silent() would skip the request log.
	var ran atomic.Bool
	logs := &logBuffer{}
	logger := zerolog.New(logs)

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
	require.Contains(t, logs.String(), "REQUEST MATCHED GET /x")
	require.NotContains(t, logs.String(), "panic")
}

func TestLifecycle_RepliesWithoutAResponseAreLogged(t *testing.T) {
	// #24: Echo, Handler and a Func that returns no response write their own response. Both request logs read that
	// response and panicked on every such request: the recovery middleware tried to answer 418 after the reply had
	// answered, and the log held a stack trace instead of the match.
	replies := []struct {
		name  string
		reply ReplierFactory
		want  int
	}{
		{
			name:  "echo",
			reply: Echo(),
			want:  http.StatusOK,
		},
		{
			name:  "handler",
			reply: Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }),
			want:  http.StatusCreated,
		},
		{
			name: "func without a response",
			reply: Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
				w.WriteHeader(http.StatusAccepted)
				return nil, nil
			}),
			want: http.StatusAccepted,
		},
	}

	for _, rc := range replies {
		t.Run("structured/"+rc.name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Getf("/x").ID("x").Reply(rc.reply))

			status, _ := get(t, m, "/x")
			require.Equal(t, rc.want, status)
			require.Contains(t, logs.String(), "REQUEST MATCHED GET /x")
			require.Contains(t, logs.String(), `"mock":{"id":"x"}`)
			require.NotContains(t, logs.String(), "panic")
		})
	}

	for _, rc := range replies {
		t.Run("descriptive/"+rc.name, func(t *testing.T) {
			// The descriptive log, and the recovery middleware's, go to os.Stdout, which the server takes when it's made.
			r, w, err := os.Pipe()
			require.NoError(t, err)

			stdout := os.Stdout
			os.Stdout = w
			m := MustNew(Setup().UseDescriptiveLogger().Colors(false).LogVerbosity(vlatte.LogBody))
			os.Stdout = stdout

			m.MustStart()
			m.MustMock(Getf("/x").ID("x").Reply(rc.reply))

			status, _ := get(t, m, "/x")
			require.Equal(t, rc.want, status)

			m.Close()
			require.NoError(t, w.Close())

			out, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Contains(t, string(out), "REQUEST MATCHED")
			require.Contains(t, string(out), "Mock: x")
			require.NotContains(t, string(out), "Status:")
			require.NotContains(t, string(out), "panic")
		})
	}
}

func TestLifecycle_LogsWhatTheVerbosityAsksFor(t *testing.T) {
	// Both request logs print the parts of the response the verbosity names: the headers and trailers, then the body.
	// The descriptive log doesn't print an encoded body.
	mocks := func(m *Server) {
		m.MustMock(
			Getf("/plain").Reply(OK().Header("X-Head", "h").Trailer("X-Tail", "t").PlainText("plain body")),
			Getf("/zipped").Reply(OK().PlainText("zipped body").Gzip()),
		)
	}

	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
		m.MustStart()
		t.Cleanup(m.Close)
		mocks(m)

		get(t, m, "/plain")
		get(t, m, "/zipped")

		out := logs.String()
		require.Contains(t, out, `"X-Head":["h"]`)
		require.Contains(t, out, `"trailer":{"X-Tail":["t"]}`)
		require.Contains(t, out, `"body":"plain body"`)
		require.Contains(t, out, `"encoding":"gzip"`)
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
		mocks(m)

		get(t, m, "/plain")
		get(t, m, "/zipped")

		m.Close()
		require.NoError(t, w.Close())

		out, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Contains(t, string(out), "Headers: ")
		require.Contains(t, string(out), "X-Head")
		require.Contains(t, string(out), "Trailers: ")
		require.Contains(t, string(out), "X-Tail")
		require.Contains(t, string(out), "Body: plain body")
		require.Contains(t, string(out), "Body: <encoded body omitted>")
	})
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

type tenantKey struct{}

// tenantKV is a parameter store scoped to the tenant of each request: a read without one fails.
type tenantKV struct {
	vlatte.KV
}

func (kv tenantKV) Get(ctx context.Context, k string) ([]byte, error) {
	if ctx.Value(tenantKey{}) == nil {
		return nil, errors.New("no tenant")
	}

	return kv.KV.Get(ctx, k)
}

func TestHandler_EventsSeeTheValuesOfTheRequestContext(t *testing.T) {
	// Middlewares put request-scoped values in the request's context, such as a trace or a tenant, which listeners and
	// parameter stores read: the events of the request see them.
	kv := tenantKV{vlatte.NewMemoryKV()}
	require.NoError(t, kv.Set(t.Context(), "target", []byte("api")))

	type seen struct {
		tenant   any
		rendered string
	}

	got := make(chan seen, 1)

	m := MustNew(Setup().Silent().Parameters(kv).
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, "acme")))
			})
		}).
		OnCompleteListener("reads", vlatte.EventFunc[vlatte.OnComplete](func(ctx context.Context, e vlatte.OnComplete) error {
			var b strings.Builder

			tmpl, err := e.Kit().TemplateEngine().Compile("target", `{{ .Parameters.Get "target" }}`)
			if err == nil {
				err = tmpl.Execute(&b, e.Kit().Data())
			}

			if err != nil {
				b.WriteString(err.Error())
			}

			got <- seen{tenant: ctx.Value(tenantKey{}), rendered: b.String()}

			return nil
		})))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").OnCompleteEvent(vlatte.EventConfig{Name: "reads"}).Reply(NoContent()))

	status, _ := get(t, m, "/x")
	require.Equal(t, http.StatusNoContent, status)

	s := <-got
	require.Equal(t, "acme", s.tenant)
	require.Equal(t, "api", s.rendered)
}

func TestHandler_DebugHeadersNameTheMockThatMatched(t *testing.T) {
	// A client sees which mock answered it, how often that mock did, and which file to edit, without the server's logs.
	path := filepath.Join(t.TempDir(), "users.mock.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"id": "users", "name": "list users", "request": {"path": "/users"}}`), 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromFile(path), Getf("/plain").ID("plain").Reply(OK()), Getf("/fails").ID("fails").Reply(OK().BodyFile("missing.txt")))

	_, header, _ := getFull(t, m, "/users")
	require.Equal(t, "users", header.Get(vlatte.MatchedMockIDHeader))
	require.Equal(t, "list users", header.Get(vlatte.MatchedMockNameHeader))
	require.Equal(t, "1", header.Get(vlatte.MatchedMockHitsHeader), "the hits count this request")
	require.Equal(t, path, header.Get(vlatte.MatchedMockSourceHeader))

	_, header, _ = getFull(t, m, "/users")
	require.Equal(t, "2", header.Get(vlatte.MatchedMockHitsHeader))

	_, header, _ = getFull(t, m, "/plain")
	require.Equal(t, "plain", header.Get(vlatte.MatchedMockIDHeader))
	require.Equal(t, "1", header.Get(vlatte.MatchedMockHitsHeader), "each mock counts its own")
	require.Empty(t, header.Values(vlatte.MatchedMockNameHeader), "a mock with no name sends none")
	require.Empty(t, header.Values(vlatte.MatchedMockSourceHeader), "nor a mock built in code a source")

	_, header, _ = getFull(t, m, "/fails")
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.Equal(t, "fails", header.Get(vlatte.MatchedMockIDHeader), "a reply that fails names its mock too")
}

func TestHandler_RequestIDHeaderFindsTheRequestInTheLogs(t *testing.T) {
	// The header carries the ID that templates and logs see, on every answer, failures and no-match included, so a
	// client finds its request in the logs.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/id").Reply(OK().BodyTemplate(`{{ .Request.ID }}`)), Getf("/fails").Reply(OK().BodyFile("missing.txt")))

	_, header, body := getFull(t, m, "/id")
	id := header.Get(vlatte.RequestIDHeader)
	require.NotEmpty(t, id)
	require.Equal(t, body, id, "templates see the same ID")
	require.Contains(t, logs.String(), `"request_id":"`+id+`"`)

	seen := map[string]bool{id: true}
	for _, path := range []string{"/fails", "/nothing"} {
		_, header, _ := getFull(t, m, path)
		id := header.Get(vlatte.RequestIDHeader)
		require.NotEmpty(t, id, path)
		require.False(t, seen[id], "%s: each request has an ID of its own", path)
		require.Contains(t, logs.String(), `"request_id":"`+id+`"`, path)

		seen[id] = true
	}
}

func TestHandler_ProxiedHeaderMarksTheAnswersOfARealService(t *testing.T) {
	// A proxied answer looks like a mock's: only the header tells the client that a real service sent it.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "real") })

	t.Run("a mock that forwards", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		m.MustStart()
		t.Cleanup(m.Close)

		m.MustMock(Getf("/forward").ID("forward").Reply(From(target.URL)), Getf("/mocked").Reply(OK()))

		_, header, body := getFull(t, m, "/forward")
		require.Equal(t, "real", body)
		require.Equal(t, "true", header.Get(vlatte.ProxiedHeader))
		require.Equal(t, "forward", header.Get(vlatte.MatchedMockIDHeader), "it names its mock")

		_, header, _ = getFull(t, m, "/mocked")
		require.Empty(t, header.Values(vlatte.ProxiedHeader), "a mock's own answer isn't proxied")
	})

	t.Run("a request no mock matches, on a proxy", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy())
		m.MustStart()
		t.Cleanup(m.Close)

		proxyURL, err := url.Parse(m.URL())
		require.NoError(t, err)

		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		t.Cleanup(transport.CloseIdleConnections)

		res, err := (&http.Client{Transport: transport}).Get(target.URL + "/other")
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, "true", res.Header.Get(vlatte.ProxiedHeader))
		require.Empty(t, res.Header.Values(vlatte.MatchedMockIDHeader), "no mock matched")
	})

	t.Run("a recording's target", func(t *testing.T) {
		m := mockedServer(t, Setup().Silent())
		require.NoError(t, m.StartRecording(RecordDir(t.TempDir()), RecordTarget(target.URL)))

		res, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
		require.Equal(t, "real", string(body))
		require.Equal(t, "true", res.Header.Get(vlatte.ProxiedHeader))

		stopRecording(t, m)
	})
}

func TestHandler_NoDebugHeadersTurnsThemAllOff(t *testing.T) {
	// The switch is all or nothing, such as for a client that compares whole answers. A failure still tells itself
	// apart from a mock's answer: that header isn't a debug one.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "real") })

	m := MustNew(Setup().Silent().NoDebugHeaders())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Getf("/x").ID("x").Name("x").SetSource("mocks/x.yaml").Reply(OK()),
		Getf("/forward").Reply(From(target.URL)),
	)

	for _, path := range []string{"/x", "/forward", "/nothing"} {
		_, header, _ := getFull(t, m, path)
		for _, name := range _debugHeaders {
			require.Empty(t, header.Values(name), "%s: %s", path, name)
		}
	}

	_, header, _ := getFull(t, m, "/nothing")
	require.Equal(t, vlatte.FailureNoMatch, header.Get(HeaderVlatteError))
}

func TestHandler_StepsDropTheContentLengthOfAReplyThatWritesItsOwn(t *testing.T) {
	// A step can change how many bytes go out, so the Content-Length a reply declares would be wrong: the server
	// failed the write. Steps drop it, whatever reply set it, a proxy's or one that writes its own response.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/h").Step(appendStep("<step>")).Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentLength, "5")
		_, _ = io.WriteString(w, "hello")
	})))

	status, body := get(t, m, "/h")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "hello<step>", body)
}
