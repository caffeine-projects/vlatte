package vlatte

import (
	"context"
	"iter"
)

type Bean interface {
	ID() string
	Name() string
	Priority() int
	Origin() string
	Ext() any
	Inc()
	Hits() int64
	Called() bool
	Roasted() bool
	Roast()
	Raw()

	Events() Events
	Callbacks() []Callback
	Steps() []Step

	String() string
}

// Grinder describes a Mock builder.
type Grinder[B Bean, TMockApp MockApp[B]] interface {
	Grind(app TMockApp) (B, error)
}

// MockBuilder knows how to turn a raw mock definition (content + extension)
// into a concrete [Bean] for a specific mock server implementation.
//
// It exists so generic tooling (such as the admin API) can stay
// transport-agnostic: each mock server (HTTP, gRPC, ...) plugs in its own
// builder that understands its own mock format.
type MockBuilder[B Bean] interface {
	BuildFromBytes(content []byte, extension string) (B, error)
}

// MockBuilderFunc adapts a plain function to the [MockBuilder] interface.
type MockBuilderFunc[B Bean] func(content []byte, extension string) (B, error)

// BuildFromBytes implements [MockBuilder].
func (f MockBuilderFunc[B]) BuildFromBytes(content []byte, extension string) (B, error) {
	return f(content, extension)
}

type Canister[T Bean] interface {
	Scoop(ctx context.Context) (iter.Seq[T], error)

	Store(ctx context.Context, bean ...T) error

	Pick(ctx context.Context, id string) (T, error)
	PickAll(ctx context.Context) ([]T, error)

	Toss(ctx context.Context, ids ...string) error
	Empty(ctx context.Context) error
}
