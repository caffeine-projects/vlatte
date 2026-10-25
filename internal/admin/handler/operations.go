package handler

import (
	"context"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var _ mocks = (*storeMocks[vlatte.Mock])(nil)

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
	mocks     mocks
	kind      string
	url       func() string
	files     Files
	recorder  Recorder
	scenarios Scenarios
	journal   Journal
	pattern   RequestPattern
}

// Capabilities are what a mock server gives its admin operations besides its mocks. A nil one is one it lacks.
type Capabilities struct {
	// Kind is the kind of the mock server, such as http.
	Kind string

	// URL returns where the server serves its mocks: its URL, or its address for gRPC.
	URL func() string

	Files     Files
	Recorder  Recorder
	Scenarios Scenarios
	Journal   Journal

	// Pattern makes the filter of the request part of a mock of the server's protocol, for [Operations.QueryRequests].
	// Nil, for a
	// protocol that has none, takes no request part.
	Pattern RequestPattern
}

// NewOperations returns the admin operations of a mock server on the mocks of store, which build makes from
// documents, and on caps. With a nil build, the server makes no mocks from documents: [Operations.AddMock],
// [Operations.ValidateMock] and [Operations.ReplaceMock] need a server that does.
func NewOperations[M vlatte.Mock](store vlatte.Store[M], build vlatte.MockBuilder[M], caps Capabilities) *Operations {
	return &Operations{
		mocks:     &storeMocks[M]{store: store, build: build},
		kind:      caps.Kind,
		url:       caps.URL,
		files:     caps.Files,
		recorder:  caps.Recorder,
		scenarios: caps.Scenarios,
		journal:   caps.Journal,
		pattern:   caps.Pattern,
	}
}

// mocks are the operations on the mocks of a server, whatever their type: storeMocks does them on a store of one.
type mocks interface {
	list(ctx context.Context) (MockList, error)
	get(ctx context.Context, id string) (descriptor.Descriptor, error)
	add(ctx context.Context, doc []byte, format string) (MockSummary, error)
	validate(ctx context.Context, doc []byte, format string) (MockSummary, error)
	replace(ctx context.Context, id string, doc []byte, format string) (MockSummary, error)
	update(ctx context.Context, id string, doc []byte, format string) (MockSummary, error)
	remove(ctx context.Context, id string) error
	removeAll(ctx context.Context) error
	reset(ctx context.Context, files Files, scenarios Scenarios, journal Journal) error
}

// storeMocks does the operations on the mocks of store, which build makes from documents.
type storeMocks[M vlatte.Mock] struct {
	store vlatte.Store[M]
	build vlatte.MockBuilder[M]
}
