// Package adminapi is what a mock server and its admin API share: what the server gives the admin API, its
// [Capabilities], and the [API] that serves them. Mock servers import it, never the admin API itself, so a server
// without an admin API links neither the admin server nor what it serves with, such as MCP.
package adminapi

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Origin is the origin of the mocks the admin API adds. A mock server's builder gives it to the mocks it builds for
// the admin API, so the admin API tells them from mocks of files and code.
const Origin = "admin"

// ErrNotDescribed is the error of a mock that can't be described as a mock file, such as one with a Handler reply.
var ErrNotDescribed = errors.New("admin: the mock can't be described as a mock file")

// Files is a mock server that serves mock files.
type Files interface {
	// Reload loads the mocks of the server's sources again, its mock files and its persistent store, in place of the
	// mocks they gave before.
	Reload() error

	// SaveMocks writes the mocks the admin API added as mock files, in format, into dir, a local path that starts at
	// the server's root directory, then serves them from the files, which load from then on. Empty dir and format are
	// the server's defaults. It returns the files' paths. An error that wraps ErrNotDescribed wrote nothing, and one
	// of files that failed to load put them back as they were.
	SaveMocks(ctx context.Context, dir, format string) ([]string, error)
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
	// Scenarios returns every scenario, by name, with its state, the states its mocks name, and their moves.
	Scenarios(ctx context.Context) ([]matchscenario.Scenario, error)

	// SetScenario moves the scenario name to state. It returns an error that wraps matchscenario.ErrUnknownState when
	// no mock of the scenario names state.
	SetScenario(ctx context.Context, name, state string) error

	// ResetScenario moves the scenario name back to where it started.
	ResetScenario(name string)

	// ResetScenarios moves every scenario back to where it started.
	ResetScenarios()
}

// Capabilities are what a mock server gives its admin API. A nil one is one it lacks: the operations that need it
// answer that the server can't do them.
type Capabilities struct {
	// Kind is the kind of the mock server, such as http.
	Kind string

	// URL returns where the server serves its mocks: its URL, or its address for gRPC.
	URL func() string

	// Logger logs the admin API's internal errors. Nil logs nothing.
	Logger *zerolog.Logger

	// Mocks are the mocks of the server. The admin API needs them.
	Mocks Mocks

	Files     Files
	Recorder  Recorder
	Scenarios Scenarios
	Events    *Events
	Logs      *Logs
	Journal   Journal

	// Pattern makes the filter of the request part of a mock of the server's protocol, for a query of the journal.
	// Nil, for a protocol that has none, takes no request part.
	Pattern RequestPattern

	// Matchers builds the matcher lists of a query of the journal, as it does the server's mock files. Nil builds the
	// built-in matchers only.
	Matchers *matchbuild.Registry

	// MockSchema is the JSON Schema of the server's mock files, a JSON document, for clients that write them.
	MockSchema []byte
}

// Server is the admin API of one mock server.
type Server interface {
	// Start serves the admin API. It starts once.
	Start() error

	// URL returns the base URL of the admin API, or "" before it starts.
	URL() string

	// Close stops the admin API, and waits for the operations it runs. A closed server doesn't start again.
	Close() error
}

// API is an admin API a mock server opts into: it serves the capabilities of the server.
type API interface {
	// Serve returns the admin API of the mock server of caps, not started yet.
	Serve(caps Capabilities) (Server, error)
}
