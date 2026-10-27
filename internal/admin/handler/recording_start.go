package handler

import (
	"errors"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// StartRecording starts a recording with the settings of doc, a document in format with the keys of the record
// section of the server's settings, and returns its state. It needs a server that records.
func (o *Operations) StartRecording(doc []byte, format string) (RecordingState, error) {
	if o.recorder == nil {
		return RecordingState{}, notSupported(_lackingRecorder)
	}

	err := o.recorder.StartRecording(doc, format)

	switch {
	case errors.Is(err, vlatte.ErrRecording):
		return RecordingState{}, &problemError{
			status: http.StatusConflict, code: _codeRecordingActive,
			detail: "a recording runs already: stop it with DELETE /recording",
		}
	case len(configProblems(err)) > 0:
		return RecordingState{}, invalid("the body", err)
	case err != nil:
		return RecordingState{}, err
	default:
		return statusOf(o.recorder.RecordingStatus()), nil
	}
}
