package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestStartRecordingTool(t *testing.T) {
	// The settings reach the recorder as a JSON document of the record section's keys.
	rec := &fakeRecorder{}
	cs := connect(t, newStore(t), adminapi.Capabilities{Recorder: rec})

	doc := conformsAs(t, call(t, cs, "start_recording", map[string]any{"settings": map[string]any{"target": "https://api.example.com"}}), "json", "RecordingStatus")
	require.Equal(t, true, doc["active"])
	require.JSONEq(t, `{"target": "https://api.example.com"}`, rec.doc)
	require.Equal(t, "json", rec.format)
}

func TestStartRecordingTool_ARecordingRuns(t *testing.T) {
	cs := connect(t, newStore(t), adminapi.Capabilities{Recorder: &fakeRecorder{startErr: vlatte.ErrRecording}})

	callFails(t, cs, "start_recording", nil, http.StatusConflict, "recording_active")
}
