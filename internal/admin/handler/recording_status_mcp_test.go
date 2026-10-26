package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestRecordingStatusTool(t *testing.T) {
	rec := &fakeRecorder{status: vlatte.RecordStatus{Active: true, Calls: 3, Mocks: 2}}
	cs := connect(t, newStore(t), adminapi.Capabilities{Recorder: rec})

	doc := conformsAs(t, call(t, cs, "recording_status", nil), "json", "RecordingStatus")
	require.Equal(t, true, doc["active"])
	require.Equal(t, int64(3), doc["calls"])
	require.Equal(t, int64(2), doc["mocks"])
}
