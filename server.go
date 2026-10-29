package vlatte

import (
	"context"
	"errors"
)

var (
	// ErrStarted is returned when a server that started starts again: a server starts once.
	ErrStarted = errors.New("server: already started")

	// ErrOutboundRefused is the error of a connection a server refused to open, to an address its outbound rules
	// refuse: see OutboundAllow in [github.com/caffeine-projects/vlatte/mockhttp.Config] and in
	// [github.com/caffeine-projects/vlatte/mockgrpc.Config].
	ErrOutboundRefused = errors.New("server: the address is refused by the outbound rules")
)

// Server is the interface every mock server implements.
// It covers both mock registration and lifecycle management.
type Server[B Mock, A any] interface {
	Hits() int64
	Enable()
	Disable()
	Clean(ctx context.Context) error
	Mock(builders ...Builder[B, A]) (*Scope[B], error)
}
