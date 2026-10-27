package mockhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestLifecycle_LogsTheStatusTheClientGot(t *testing.T) {
	// A middleware may answer in place of the mock: the request log has the status the client got, as the journal
	// and the events do, not the one the mock's response holds.
	t.Run("structured", func(t *testing.T) {
		logs := &logBuffer{}
		logger := zerolog.New(logs)

		m := MustNew(Setup().Logger(&logger).Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			next.ServeHTTP(&rewriting{ResponseWriter: w, status: http.StatusCreated}, r)
		})))
		m.MustStart()
		t.Cleanup(m.Close)
		m.MustMock(Getf("/made").Reply(OK()))

		res, _ := do(t, http.MethodGet, m.URL("/made"), nil, nil)
		require.Equal(t, http.StatusCreated, res.StatusCode)
		require.Contains(t, logs.String(), `"response":{"status":201`)
	})

	t.Run("descriptive", func(t *testing.T) {
		// New writes the descriptive log to stdout, so the test builds it with a buffer.
		m := MustNew()
		var out bytes.Buffer
		lifecycle := newBuiltInDescriptiveMockHTTPLifecycle(m, ansi(false), &out)

		mock, err := Getf("/made").ID("made").Reply(OK()).Build(m)
		require.NoError(t, err)

		res := newResponse()
		res.StatusCode = http.StatusOK

		req := httptest.NewRequest(http.MethodGet, "/made", nil)
		answer := &answerWriter{rw: httptest.NewRecorder()}
		answer.WriteHeader(http.StatusCreated)

		lifecycle.OnMatch(&RequestValues{ReqID: "r1", RawRequest: req, URL: req.URL, Mock: mock, answer: answer}, res)
		require.Contains(t, out.String(), "Status: 201\n")
	})
}
