package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestStopRecordingTool(t *testing.T) {
	rec := &fakeRecorder{result: &vlatte.RecordResult{
		Recordings: []vlatte.Recording{{ID: "get-items", File: "mocks/GET-items.mock.json", Responses: 1}},
	}}
	cs := connect(t, newStore(t), adminapi.Capabilities{Recorder: rec})

	doc := conformsAs(t, call(t, cs, "stop_recording", nil), "json", "RecordingResult")
	require.Equal(t, []any{map[string]any{"id": "get-items", "file": "mocks/GET-items.mock.json", "responses": int64(1)}}, doc["recordings"])
}

func TestStopRecordingTool_NoneRuns(t *testing.T) {
	cs := connect(t, newStore(t), adminapi.Capabilities{Recorder: &fakeRecorder{stopErr: vlatte.ErrNotRecording}})

	callFails(t, cs, "stop_recording", nil, http.StatusConflict, "not_recording")
}
