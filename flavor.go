package vlatte

import "context"

// Flavor is the interface all mock flavor implementations must satisfy.
// It covers both mock registration and lifecycle management.
type Flavor[B Mock, A any] interface {
	Hits() int64
	Enable()
	Disable()
	Clean(ctx context.Context) error
	Mock(builders ...Builder[B, A]) (*Scope[B], error)
}
