package vlatte

import (
	"context"
	"iter"
)

// Mock is what the core needs from a protocol's mock: identity, ordering, state, hits and events.
// Protocol packages add what only they use, such as callbacks or steps, on their own mock types.
//
// A mock that holds something to release, such as the connections of an HTTP client, implements io.Closer: the store
// closes it when it leaves, and a builder's mock that never makes it into the store is closed too.
type Mock interface {
	ID() string
	Name() string
	Priority() int
	Hits() int64
	Called() bool
	Enabled() bool
	Enable()
	Disable()
	Events() Events
	String() string
}

// Builder describes a Mock builder. Build returns a new mock on each call and must not change the builder, so a builder
// can be built for several servers, or more than once for one.
type Builder[B Mock, TMockApp any] interface {
	Build(app TMockApp) (B, error)
}

// MockBuilder knows how to turn a raw mock definition (content + extension)
// into a concrete [Mock] for a specific mock server implementation.
//
// It exists so generic tooling (such as the admin API) can stay
// transport-agnostic: each mock server (HTTP, gRPC, ...) plugs in its own
// builder that understands its own mock format.
type MockBuilder[B Mock] interface {
	BuildFromBytes(content []byte, extension string) (B, error)
}

// MockBuilderFunc adapts a plain function to the [MockBuilder] interface.
type MockBuilderFunc[B Mock] func(content []byte, extension string) (B, error)

// BuildFromBytes implements [MockBuilder].
func (f MockBuilderFunc[B]) BuildFromBytes(content []byte, extension string) (B, error) {
	return f(content, extension)
}

// Store holds the mocks of a server. Replace, Delete and Clear close the mocks they remove that implement io.Closer,
// once the store is unlocked; a mock Replace stores again stays open.
type Store[T Mock] interface {
	List(ctx context.Context) (iter.Seq[T], error)

	// Store adds mocks. It fails with ErrDuplicateID, storing none of them,
	// when an ID is already stored or repeated in the call.
	Store(ctx context.Context, mocks ...T) error

	// Replace removes the mocks of old it holds and stores mocks, in one step: a reader finds the mocks of old or
	// the new ones, never neither. Mocks are told apart by identity, so a mock of old that left the store stays out,
	// even when its ID now names another mock; Mock implementations must be comparable, as pointers are.
	// It fails with ErrDuplicateID, changing nothing, when an ID of mocks is held by a mock it keeps,
	// or repeated in the call.
	Replace(ctx context.Context, old []T, mocks ...T) error

	Pick(ctx context.Context, id string) (T, error)
	PickAll(ctx context.Context) ([]T, error)

	Delete(ctx context.Context, ids ...string) error
	Clear(ctx context.Context) error
}
