package recovery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecover(t *testing.T) {
	// A panic answers with the status and header the server chose for failures, so a client tells it from a mock's
	// answer, and the body says what panicked.
	msg := "error test"
	fn := func(w http.ResponseWriter, r *http.Request) {
		panic(msg)
	}

	var logged error
	ts := httptest.NewServer(New(func(err error) { logged = err },
		http.StatusInternalServerError, http.Header{"X-Failure": {"panic"}}).Recover(http.HandlerFunc(fn)))
	defer ts.Close()

	res, err := http.Get(ts.URL + "/p")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", res.Header.Get("content-type"))
	require.Equal(t, "panic", res.Header.Get("X-Failure"))
	require.Equal(t, "PANIC\nGET /p\n\n"+msg, string(body))
	require.ErrorContains(t, logged, "http: panic serving GET /p: "+msg)
}

func TestRecover_AbortHandlerPanicsAgain(t *testing.T) {
	// net/http aborts a response by panicking with http.ErrAbortHandler, as the reverse proxy does when the client
	// goes away. Answering it would write to a response that is being dropped, so it must reach net/http.
	h := New(func(error) { t.Error("an abort is not a failure to log") }, http.StatusTeapot, nil).
		Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))

	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	})
}
