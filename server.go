package vlatte

import (
	"context"
	"errors"
)

// ErrStarted is returned when a server that started starts again: a server starts once.
var ErrStarted = errors.New("server: already started")

// Server is the interface every mock server implements.
// It covers both mock registration and lifecycle management.
type Server[B Mock, A any] interface {
	Hits() int64
	Enable()
	Disable()
	Clean(ctx context.Context) error
	Mock(builders ...Builder[B, A]) (*Scope[B], error)
}
