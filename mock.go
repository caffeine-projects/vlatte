package vlatte

import (
	"context"
	"iter"
)

// Mock is what the core needs from a protocol's mock: identity, ordering, state, hits and events.
// Protocol packages add what only they use, such as callbacks or steps, on their own mock types.
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

// Builder describes a Mock builder.
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

type Store[T Mock] interface {
	List(ctx context.Context) (iter.Seq[T], error)

	Store(ctx context.Context, mocks ...T) error

	Pick(ctx context.Context, id string) (T, error)
	PickAll(ctx context.Context) ([]T, error)

	Delete(ctx context.Context, ids ...string) error
	Clear(ctx context.Context) error
}
