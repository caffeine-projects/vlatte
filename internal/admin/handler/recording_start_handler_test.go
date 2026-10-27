package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

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
			url := serve(t, newStore(t), adminapi.Capabilities{Recorder: rec})

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
			url := serve(t, newStore(t), adminapi.Capabilities{Recorder: &fakeRecorder{startErr: tt.err}})

			res, body := send(t, request{method: http.MethodPost, url: url + "/recording", body: `{"format": "xml"}`})

			doc := problem(t, res, body, tt.wantStatus, tt.wantCode)
			if tt.wantCode == "invalid_body" {
				require.Equal(t, "the body has 1 problem", doc["detail"])
				require.Equal(t, "format", doc["problems"].([]any)[0].(map[string]any)["key"])
			}
		})
	}
}
