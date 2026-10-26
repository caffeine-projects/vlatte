package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestRecording_Status(t *testing.T) {
	rec := &fakeRecorder{status: vlatte.RecordStatus{Active: true, Calls: 3, Mocks: 2, Dropped: 1}}
	url := serve(t, newStore(t), adminapi.Capabilities{Recorder: rec})

	res, body := send(t, request{method: http.MethodGet, url: url + "/recording"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	doc := conforms(t, res, body, "RecordingStatus")
	require.Equal(t, map[string]any{"active": true, "calls": int64(3), "mocks": int64(2), "dropped": int64(1), "failed": int64(0)}, doc)
}

func TestRecording_NotSupported(t *testing.T) {
	// A server that can't record says so, and the recorder page shows it.
	url := serve(t, newStore(t))

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		res, body := send(t, request{method: method, url: url + "/recording"})

		doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
		require.Equal(t, "this mock server can't record", doc["detail"])
	}

	res, body := send(t, request{method: http.MethodGet, url: url + "/recorder"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))
	require.Contains(t, body, "call('POST', 'recording'")
}

func TestRecordingStatus_OnOperationsThatCantRecord(t *testing.T) {
	// The handler, mounted apart from the admin API's routes on a server that can't record, answers what its route
	// would: a 501 problem, never a status of a recorder it lacks.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/recording", nil)
	rec := httptest.NewRecorder()

	handler.RecordingStatus(ops).ServeHTTP(rec, req)

	res := rec.Result()
	res.Request = req
	doc := problem(t, res, rec.Body.String(), http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't record", doc["detail"])
}
