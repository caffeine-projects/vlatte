package adminapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStream_AWriterThatCannotFlushEndsTheStream(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := struct{ http.ResponseWriter }{ResponseWriter: recorder}
	newStream().serve(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, "", recorder.Body.String())
}
