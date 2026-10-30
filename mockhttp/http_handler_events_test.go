package mockhttp

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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

func TestLifecycle_DescriptiveLoggingIsSilentAtTheDisabledLevel(t *testing.T) {
	m := MustNew(Setup().Silent())
	mock, err := Getf("/tea").Reply(OK()).Build(m)
	require.NoError(t, err)
	raw := httptest.NewRequest(http.MethodGet, "/tea", nil)
	r := &RequestValues{RawRequest: raw, URL: raw.URL, Mock: mock, answer: &answerWriter{rw: httptest.NewRecorder()}}
	errEvent := errors.New("test: event failed")

	tests := []struct {
		name string
		emit func(mockHTTPLifecycle)
	}{
		{name: "request", emit: func(l mockHTTPLifecycle) { l.OnRequest(r) }},
		{name: "matched", emit: func(l mockHTTPLifecycle) { l.OnMatch(r, newResponse()) }},
		{name: "proxied", emit: func(l mockHTTPLifecycle) { l.OnProxied(r, newResponse()) }},
		{name: "unmatched", emit: func(l mockHTTPLifecycle) { l.OnNoMatch(r) }},
		{name: "warning", emit: func(l mockHTTPLifecycle) { l.OnWarning(r, errEvent) }},
		{name: "error", emit: func(l mockHTTPLifecycle) { l.OnError(r, errEvent) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.emit(newBuiltInDescriptiveMockHTTPLifecycle(m, ansi(false), &out))
			require.Equal(t, "", out.String())
		})
	}
}

func TestLifecycle_RequestBodiesAppearAtBodyVerbosity(t *testing.T) {
	raw := httptest.NewRequest(http.MethodPost, "http://example.org/tea", nil)
	r := &RequestValues{ReqID: "r1", StartedAtTime: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), RawRequest: raw, URL: raw.URL, RawBodyBytes: []byte("tea")}
	var structured bytes.Buffer
	logger := zerolog.New(&structured)
	m := MustNew(Setup().Logger(&logger).LogVerbosity(vlatte.LogBody))
	newBuiltInMockHTTPLifecycle(m).OnRequest(r)
	require.JSONEq(t, `{"level":"info","url":"http://example.org/tea","request_id":"r1","request":{"header":{},"body":"tea"},"message":"---> REQUEST RECEIVED POST /tea"}`, structured.String())

	var descriptive bytes.Buffer
	newBuiltInDescriptiveMockHTTPLifecycle(m, ansi(false), &descriptive).OnRequest(r)
	require.Equal(t, "REQUEST RECEIVED 2026-10-30T00:00:00Z ---> POST /tea\nPOST http://example.org/tea\nRequest ID: r1\nBody: tea\n\n", descriptive.String())
}
