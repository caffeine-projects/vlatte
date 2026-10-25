package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// RecordingResult is what a recording wrote, and what it couldn't.
type RecordingResult struct {
	Status     RecordingState `json:"status"`
	Recordings []RecordedMock `json:"recordings"`
	Errors     []string       `json:"errors,omitempty"`
}

// RecordedMock is a mock file a recording wrote.
type RecordedMock struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	File      string   `json:"file"`
	BodyFiles []string `json:"body_files,omitempty"`
	Responses int      `json:"responses"`
}

// StopRecording stops the recording, waits until it wrote what it took, and returns what it wrote, which the server
// then serves. What it couldn't record, or serve, is in the result's errors: the recording stopped all the same. It
// needs a server that records.
func (o *Operations) StopRecording(ctx context.Context) (RecordingResult, error) {
	if o.recorder == nil {
		return RecordingResult{}, notSupported(_lackingRecorder)
	}

	res, err := o.recorder.StopRecording(ctx)

	switch {
	case errors.Is(err, vlatte.ErrNotRecording):
		return RecordingResult{}, &problemError{
			status: http.StatusConflict, code: _codeNotRecording, detail: "no recording runs: start one with POST /recording",
		}
	case res == nil:
		return RecordingResult{}, err
	}

	result := RecordingResult{Status: statusOf(res.Status), Recordings: make([]RecordedMock, 0, len(res.Recordings))}
	for _, m := range res.Recordings {
		result.Recordings = append(result.Recordings, RecordedMock(m))
	}

	if err != nil {
		result.Errors = messages(err)
	}

	return result, nil
}

// messages returns the messages of the errors err joins, or its own.
func messages(err error) []string {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []string{err.Error()}
	}

	var out []string
	for _, e := range joined.Unwrap() {
		out = append(out, messages(e)...)
	}

	return out
}
