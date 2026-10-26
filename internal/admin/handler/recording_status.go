package handler

import (
	"github.com/caffeine-projects/vlatte"
)

// RecordingState counts the calls of a recording.
type RecordingState struct {
	Active  bool  `json:"active"`
	Calls   int64 `json:"calls"`
	Mocks   int64 `json:"mocks"`
	Dropped int64 `json:"dropped"`
	Failed  int64 `json:"failed"`
}

// RecordingStatus returns the state of the recording. It needs a server that records.
func (o *Operations) RecordingStatus() (RecordingState, error) {
	if o.recorder == nil {
		return RecordingState{}, notSupported(_lackingRecorder)
	}

	return statusOf(o.recorder.RecordingStatus()), nil
}

func statusOf(s vlatte.RecordStatus) RecordingState {
	return RecordingState(s)
}
