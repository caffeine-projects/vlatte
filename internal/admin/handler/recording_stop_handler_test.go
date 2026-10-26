package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
)

func TestRecording_Stop(t *testing.T) {
	// The answer lists the mock files written. What the recording couldn't record, or serve, comes with them: it
	// stopped all the same.
	rec := &fakeRecorder{
		result: &vlatte.RecordResult{
			Status: vlatte.RecordStatus{Calls: 2, Mocks: 1, Failed: 1},
			Recordings: []vlatte.Recording{
				{ID: "get-items", File: "testdata/_mocks_recorded/GET-items.mock.json", BodyFiles: []string{"GET-items.body.json"}, Responses: 1},
			},
		},
		stopErr: errors.Join(errors.New("recorder: GET /a: too large"), errors.New("recorder: mock id already in use")),
	}
	url := serve(t, newStore(t), admin.Recorder(rec))

	res, body := send(t, request{method: http.MethodDelete, url: url + "/recording"})

	require.Equal(t, http.StatusOK, res.StatusCode, body)
	doc := conforms(t, res, body, "RecordingResult")
	require.Equal(t, []any{map[string]any{
		"id": "get-items", "file": "testdata/_mocks_recorded/GET-items.mock.json",
		"body_files": []any{"GET-items.body.json"}, "responses": int64(1),
	}}, doc["recordings"])
	require.Equal(t, []any{"recorder: GET /a: too large", "recorder: mock id already in use"}, doc["errors"])
}

func TestRecording_StopRefused(t *testing.T) {
	t.Run("none runs", func(t *testing.T) {
		url := serve(t, newStore(t), admin.Recorder(&fakeRecorder{stopErr: vlatte.ErrNotRecording}))

		res, body := send(t, request{method: http.MethodDelete, url: url + "/recording"})

		problem(t, res, body, http.StatusConflict, "not_recording")
	})

	t.Run("no result", func(t *testing.T) {
		url := serve(t, newStore(t), admin.Recorder(&fakeRecorder{stopErr: context.DeadlineExceeded}))

		res, body := send(t, request{method: http.MethodDelete, url: url + "/recording"})

		problem(t, res, body, http.StatusInternalServerError, "internal")
	})
}
