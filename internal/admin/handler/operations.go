package handler

import (
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

// What a mock server lacks the capability to do, for the problem of an operation it can't do.
const (
	_lackingBuilder   = "build mocks from documents"
	_lackingRecorder  = "record"
	_lackingScenarios = "keep scenarios"
	_lackingJournal   = "keep a journal of requests"
)

// Operations are the admin operations of a mock server, apart from how a request reaches them: each route of the
// admin API runs one. An operation that needs a capability the server lacks, such as [Operations.ReloadMocks] on a
// server without mock files, returns a not_supported problem, as its route answers 501. Bodies are documents, in JSON
// or YAML, as the routes take them. [Problem] gives the problem document of an error.
type Operations struct {
	mocks     storeMocks
	kind      string
	url       func() string
	files     adminapi.Files
	recorder  adminapi.Recorder
	scenarios adminapi.Scenarios
	journal   adminapi.Journal
	pattern   adminapi.RequestPattern
	matchers  *matchbuild.Registry
}

// NewOperations returns the admin operations of the mock server of caps, which has its Mocks.
func NewOperations(caps adminapi.Capabilities) *Operations {
	matchers := caps.Matchers
	if matchers == nil {
		matchers = matchbuild.MustNew()
	}

	return &Operations{
		mocks:     storeMocks{mocks: caps.Mocks},
		kind:      caps.Kind,
		url:       caps.URL,
		files:     caps.Files,
		recorder:  caps.Recorder,
		scenarios: caps.Scenarios,
		journal:   caps.Journal,
		pattern:   caps.Pattern,
		matchers:  matchers,
	}
}

// Builds reports whether the server builds mocks from documents, for [Operations.AddMock], [Operations.ValidateMock]
// and [Operations.ReplaceMock].
func (o *Operations) Builds() bool { return o.mocks.mocks.Builds() }

// HasFiles reports whether the server serves mock files, for [Operations.ReloadMocks] and [Operations.SaveMocks].
func (o *Operations) HasFiles() bool { return o.files != nil }

// Records reports whether the server records, for [Operations.StartRecording] and its siblings.
func (o *Operations) Records() bool { return o.recorder != nil }

// HasScenarios reports whether the server keeps scenarios, for [Operations.ListScenarios] and its siblings.
func (o *Operations) HasScenarios() bool { return o.scenarios != nil }

// HasJournal reports whether the server keeps a journal of requests, for [Operations.ListRequests] and its siblings.
func (o *Operations) HasJournal() bool { return o.journal != nil }

// storeMocks does the operations on the mocks of a mock server.
type storeMocks struct {
	mocks adminapi.Mocks
}
