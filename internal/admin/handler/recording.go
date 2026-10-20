package handler

import (
	"errors"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// recordingStatus counts the calls of a recording.
type recordingStatus struct {
	Active  bool  `json:"active"`
	Calls   int64 `json:"calls"`
	Mocks   int64 `json:"mocks"`
	Dropped int64 `json:"dropped"`
	Failed  int64 `json:"failed"`
}

// recordingResult is what a recording wrote, and what it couldn't.
type recordingResult struct {
	Status     recordingStatus `json:"status"`
	Recordings []recorded      `json:"recordings"`
	Errors     []string        `json:"errors,omitempty"`
}

// recorded is a mock file a recording wrote.
type recorded struct {
	ID        string   `json:"id"`
	Name      string   `json:"name,omitempty"`
	File      string   `json:"file"`
	BodyFiles []string `json:"body_files,omitempty"`
	Responses int      `json:"responses"`
}

// RecordingStatus answers the state of the recording: GET /recording.
func RecordingStatus(rec Recorder) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, http.StatusOK, statusOf(rec.RecordingStatus()))
	})
}

// StartRecording starts a recording with the settings of the request body, those of the record section of the
// server's settings, and answers its state: POST /recording.
func StartRecording(rec Recorder) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		err = rec.StartRecording(data, format)

		switch {
		case errors.Is(err, vlatte.ErrRecording):
			fail(w, r, &problemError{
				status: http.StatusConflict, code: _codeRecordingActive,
				detail: "a recording runs already: stop it with DELETE /recording",
			})
		case len(configProblems(err)) > 0:
			fail(w, r, invalid("the body", err))
		case err != nil:
			fail(w, r, err)
		default:
			w.Header().Set("Location", "/recording")
			respond(w, r, http.StatusCreated, statusOf(rec.RecordingStatus()))
		}
	})
}

// StopRecording stops the recording, waits until it wrote what it took, and answers what it wrote, which the server
// then serves: DELETE /recording. What it couldn't record, or serve, is in the answer's errors: the recording stopped
// all the same.
func StopRecording(rec Recorder) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		res, err := rec.StopRecording(r.Context())

		switch {
		case errors.Is(err, vlatte.ErrNotRecording):
			fail(w, r, &problemError{
				status: http.StatusConflict, code: _codeNotRecording, detail: "no recording runs: start one with POST /recording",
			})

			return
		case res == nil:
			fail(w, r, err)
			return
		}

		result := recordingResult{Status: statusOf(res.Status), Recordings: make([]recorded, 0, len(res.Recordings))}
		for _, m := range res.Recordings {
			result.Recordings = append(result.Recordings, recorded(m))
		}

		if err != nil {
			result.Errors = messages(err)
		}

		respond(w, r, http.StatusOK, result)
	})
}

func statusOf(s vlatte.RecordStatus) recordingStatus {
	return recordingStatus(s)
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
