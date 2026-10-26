package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	// errNotStarted is the error of a Dial before Start: there is nothing to dial yet.
	errNotStarted = errors.New("server: Dial: the server hasn't started")

	// errInMemoryListener is the error of a server given a listener to serve in memory, where it serves none.
	errInMemoryListener = errors.New("config: an in-memory server serves no listener: set InMemory or Listener, not both")
)

// _bufSize is the buffer of each in-memory connection.
const _bufSize = 1 << 20

// Dial returns a new client connection to the server, which Close closes: one per test, or per client of a test. It
// dials without TLS, and reaches an in-memory server too: opts come after its own, so they can change them, such as
// grpc.WithTransportCredentials for a server whose ServerOptions set TLS. It fails before Start, and after Close with
// grpc.ErrServerStopped.
func (app *Server) Dial(opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	switch {
	case app.closed:
		return nil, fmt.Errorf("server: Dial: %w", grpc.ErrServerStopped)
	case !app.started:
		return nil, errNotStarted
	}

	target := app.addr
	base := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	if app.memory != nil {
		memory := app.memory
		target = "passthrough:///" + app.addr
		base = append(base, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return memory.DialContext(ctx)
		}))
	}

	conn, err := grpc.NewClient(target, slices.Concat(base, opts)...)
	if err != nil {
		return nil, err
	}

	app.conns = append(app.conns, conn)

	return conn, nil
}

// MustDial is like Dial, but panics if it fails.
func (app *Server) MustDial(opts ...grpc.DialOption) *grpc.ClientConn {
	conn, err := app.Dial(opts...)
	if err != nil {
		panic(err)
	}

	return conn
}
