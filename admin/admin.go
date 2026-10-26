// Package admin is the admin API of a mock server: a RESTful HTTP server of its own, on its own address, that lists,
// adds and changes the server's mocks, and reads its journal, while it runs. A mock server serves none unless it is
// given one:
//
//	m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew(admin.Addr("127.0.0.1:8081"))))
//
// The same works for gRPC with mockgrpc. An Admin serves one mock server.
package admin

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	adminserver "github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

var _ adminapi.API = (*Admin)(nil)

// ErrServing is the error of an Admin given to a second mock server: it serves one.
var ErrServing = errors.New("admin: the admin API serves a mock server already")

// Admin is the admin API a mock server opts into, with ConfigBuilder.Admin of mockhttp or mockgrpc.
type Admin struct {
	opts adminserver.Options

	mu      sync.Mutex
	serving bool
}

// Option configures an Admin.
type Option func(a *Admin)

// Addr sets the address the admin API listens on: host:port, :port or a port. Empty, the default, is a random local
// port: the mock server's AdminURL returns where it listens.
func Addr(addr string) Option {
	return func(a *Admin) { a.opts.Addr = addr }
}

// New creates an admin API. It fails on an address that is neither host:port, :port nor a port.
func New(options ...Option) (*Admin, error) {
	a := &Admin{}
	for _, option := range options {
		option(a)
	}

	if err := checkAddr(a.opts.Addr); err != nil {
		return nil, err
	}

	return a, nil
}

// MustNew is like New, but panics on an error.
func MustNew(options ...Option) *Admin {
	a, err := New(options...)
	if err != nil {
		panic(err)
	}

	return a
}

// Serve returns the admin API of the mock server of caps. A mock server calls it as it is created; a second call
// fails with ErrServing.
func (a *Admin) Serve(caps adminapi.Capabilities) (adminapi.Server, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.serving {
		return nil, ErrServing
	}

	s, err := adminserver.New(a.opts, caps)
	if err != nil {
		return nil, err
	}

	a.serving = true

	return s, nil
}

func checkAddr(addr string) error {
	if addr == "" {
		return nil
	}

	if _, err := strconv.Atoi(addr); err == nil {
		return nil
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("admin: address %q: expected host:port, :port or a port number", addr)
	}

	return nil
}
