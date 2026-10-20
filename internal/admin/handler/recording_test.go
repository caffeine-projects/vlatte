package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// fakeRecorder records nothing: it keeps the settings it starts with, and answers what it is told to.
type fakeRecorder struct {
	doc      string
	format   string
	status   vlatte.RecordStatus
	startErr error
	result   *vlatte.RecordResult
	stopErr  error
}

func (r *fakeRecorder) StartRecording(doc []byte, format string) error {
	if r.startErr != nil {
		return r.startErr
	}

	r.doc, r.format = string(doc), format
	r.status.Active = true

	return nil
}

func (r *fakeRecorder) StopRecording(context.Context) (*vlatte.RecordResult, error) {
	return r.result, r.stopErr
}

func (r *fakeRecorder) RecordingStatus() vlatte.RecordStatus {
	return r.status
}

func TestRecording_Status(t *testing.T) {
	rec := &fakeRecorder{status: vlatte.RecordStatus{Active: true, Calls: 3, Mocks: 2, Dropped: 1}}
	url := serve(t, newStore(t), admin.Recorder(rec))

	res, body := send(t, request{method: http.MethodGet, url: url + "/recording"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	doc := conforms(t, res, body, "RecordingStatus")
	require.Equal(t, map[string]any{"active": true, "calls": int64(3), "mocks": int64(2), "dropped": int64(1), "failed": int64(0)}, doc)
}

func TestRecording_Start(t *testing.T) {
	// The body holds the keys of the record section of the server's settings, in JSON or YAML, and reaches the
	// recorder as it was sent.
	tests := []struct {
		name        string
		contentType string
		body        string
		wantFormat  string
	}{
		{name: "json", body: `{"target": "https://api.example.com"}`, wantFormat: "json"},
		{name: "yaml", contentType: "application/yaml", body: "target: https://api.example.com\n", wantFormat: "yaml"},
		{name: "no body", wantFormat: "json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			url := serve(t, newStore(t), admin.Recorder(rec))

			res, body := send(t, request{method: http.MethodPost, url: url + "/recording", contentType: tt.contentType, body: tt.body})

			require.Equal(t, http.StatusCreated, res.StatusCode, body)
			require.Equal(t, "/recording", res.Header.Get("Location"))
			require.Equal(t, true, conforms(t, res, body, "RecordingStatus")["active"])
			require.Equal(t, tt.body, rec.doc)
			require.Equal(t, tt.wantFormat, rec.format)
		})
	}
}

func TestRecording_StartRefused(t *testing.T) {
	// One recording runs at a time, and settings with problems say which, through the wrapping of the recorder. A
	// recorder that fails otherwise is the server's problem.
	settings := &config.Error{Problems: []config.Problem{{
		Code: config.CodeInvalidValue, Origin: "admin request", Key: "format", Message: "expected one of json, yaml, yml",
	}}}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "a recording runs", err: vlatte.ErrRecording, wantStatus: http.StatusConflict, wantCode: "recording_active"},
		{
			name: "settings with problems", err: fmt.Errorf("recorder: option at index 0: %w", settings),
			wantStatus: http.StatusBadRequest, wantCode: "invalid_body",
		},
		{
			name: "a recorder that fails", err: errors.New("recorder: broken"),
			wantStatus: http.StatusInternalServerError, wantCode: "internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := serve(t, newStore(t), admin.Recorder(&fakeRecorder{startErr: tt.err}))

			res, body := send(t, request{method: http.MethodPost, url: url + "/recording", body: `{"format": "xml"}`})

			doc := problem(t, res, body, tt.wantStatus, tt.wantCode)
			if tt.wantCode == "invalid_body" {
				require.Equal(t, "the body has 1 problem", doc["detail"])
				require.Equal(t, "format", doc["problems"].([]any)[0].(map[string]any)["key"])
			}
		})
	}
}

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
