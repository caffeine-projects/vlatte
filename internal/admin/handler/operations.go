package handler

import (
	"context"
	"errors"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

// What a mock server lacks the capability to do, for the problem of an operation it can't do.
const (
	_lackingBuilder   = "build mocks from documents"
	_lackingRecorder  = "record"
	_lackingScenarios = "keep scenarios"
	_lackingJournal   = "keep a journal of requests"
	_lackingSchema    = "describe its mock files"
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
	schema    []byte

	// waits ends with EndWaits: an operation that waits, such as VerifyRequests, waits on it too.
	waits    context.Context
	endWaits context.CancelFunc
}

// errWaitEnded is the cause of the end of the waits EndWaits ends.
var errWaitEnded = errors.New("handler: the admin API ended the wait: it is closing")

// NewOperations returns the admin operations of the mock server of caps, which has its Mocks.
func NewOperations(caps adminapi.Capabilities) *Operations {
	matchers := caps.Matchers
	if matchers == nil {
		matchers = matchbuild.MustNew()
	}

	waits, endWaits := context.WithCancelCause(context.Background())

	return &Operations{
		waits:     waits,
		endWaits:  func() { endWaits(errWaitEnded) },
		mocks:     storeMocks{mocks: caps.Mocks},
		kind:      caps.Kind,
		url:       caps.URL,
		files:     caps.Files,
		recorder:  caps.Recorder,
		scenarios: caps.Scenarios,
		journal:   caps.Journal,
		pattern:   caps.Pattern,
		matchers:  matchers,
		schema:    caps.MockSchema,
	}
}

// EndWaits ends the waits of the operations in progress, and of those to come, as the admin API closes: their
// clients get a closing problem rather than hold up Close.
func (o *Operations) EndWaits() { o.endWaits() }

// Builds reports whether the server builds mocks from documents, for [Operations.AddMock], [Operations.ValidateMock]
// and [Operations.ReplaceMock].
func (o *Operations) Builds() bool { return o.mocks.mocks.Builds() }

// HasFiles reports whether the server serves mock files, for [Operations.ReloadMocks] and [Operations.SaveMocks].
func (o *Operations) HasFiles() bool { return o.files != nil }

// Records reports whether the server records, for [Operations.StartRecording] and its siblings.
func (o *Operations) Records() bool { return o.recorder != nil }

// HasScenarios reports whether the server keeps scenarios, for [Operations.ListScenarios] and its siblings.
func (o *Operations) HasScenarios() bool { return o.scenarios != nil }

// HasMockSchema reports whether the server describes its mock files with a JSON Schema, for [Operations.MockSchema].
func (o *Operations) HasMockSchema() bool { return o.schema != nil }

// HasJournal reports whether the server keeps a journal of requests, for [Operations.ListRequests] and its siblings.
func (o *Operations) HasJournal() bool { return o.journal != nil }

// waiting returns ctx, which ends too with EndWaits, for an operation that waits, and the func that releases it.
func (o *Operations) waiting(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(o.waits, func() { cancel(context.Cause(o.waits)) })

	return ctx, func() {
		stop()
		cancel(nil)
	}
}

// storeMocks does the operations on the mocks of a mock server.
type storeMocks struct {
	mocks adminapi.Mocks
}
