// Package handler holds the units of the admin API. Each unit is an http.Handler that does its admin operation itself:
// on the store the mock server serves its mocks from, or on a capability of the server, such as its mock files.
package handler

import (
	"context"
	"io"

	"github.com/caffeine-projects/vlatte"
)

// Origin is the origin of the mocks the admin API adds. A mock server's builder gives it to the mocks it builds for
// the admin API, so the admin API tells them from mocks of files and code.
const Origin = "admin"

// Files is a mock server that serves mock files.
type Files interface {
	// Reload loads the mock files again, in place of the mocks they gave before.
	Reload() error
}

// Recorder is a mock server that records what it proxies as mock files.
type Recorder interface {
	// StartRecording starts a recording with the settings of doc, a document in format with the keys of the record
	// section of the server's settings; an empty one keeps them. It returns vlatte.ErrRecording while a recording runs,
	// and a *config.Error when doc has problems.
	StartRecording(doc []byte, format string) error

	// StopRecording stops the recording, waits until it wrote what it took, and serves it. It returns
	// vlatte.ErrNotRecording when none runs. A result with an error tells what wasn't recorded or served.
	StopRecording(ctx context.Context) (*vlatte.RecordResult, error)

	// RecordingStatus counts the calls of the recording, or is zero when none runs.
	RecordingStatus() vlatte.RecordStatus
}

// Scenarios is a mock server that keeps the state of scenarios: mocks that match in turn.
type Scenarios interface {
	// Scenarios returns the state of every scenario, by name.
	Scenarios() map[string]string

	// SetScenario moves the scenario name to state, starting it when no mock did yet.
	SetScenario(name, state string)

	// ResetScenarios moves every scenario back to where it started.
	ResetScenarios()
}

// mockSummary is what the admin API tells of a mock besides its description: its state.
type mockSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	Hits     int64  `json:"hits"`
	Origin   string `json:"origin,omitempty"`
}

func summarize(mock vlatte.Mock) mockSummary {
	s := mockSummary{ID: mock.ID(), Name: mock.Name(), Priority: mock.Priority(), Enabled: mock.Enabled(), Hits: mock.Hits()}
	if o, ok := mock.(interface{ Origin() string }); ok {
		s.Origin = o.Origin()
	}

	return s
}

// closeMock releases what a mock the store didn't take holds, such as the HTTP client of a proxy mock.
func closeMock(mock vlatte.Mock) {
	if c, ok := mock.(io.Closer); ok {
		_ = c.Close()
	}
}
