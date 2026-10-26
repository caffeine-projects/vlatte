// Package admin is the admin API of a mock server: a RESTful HTTP server of its own, on its own address, that lists,
// adds and changes the server's mocks, and reads its journal, while it runs. A mock server serves none unless it is
// given one:
//
//	m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew(admin.Addr("127.0.0.1:8081"))))
//
// The same works for gRPC with mockgrpc. An Admin serves one mock server.
//
// With [MCP], the admin API serves its operations to AI agents too, over the Model Context Protocol: a tool per
// operation, at POST /mcp of the admin API, and over stdio with [Admin.ServeMCP].
package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	adminserver "github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

var _ adminapi.API = (*Admin)(nil)

var (
	// ErrServing is the error of an Admin given to a second mock server: it serves one.
	ErrServing = errors.New("admin: the admin API serves a mock server already")

	errNotServing = errors.New("admin: the admin API serves no mock server yet: create one with it first")
	errNoMCP      = errors.New("admin: MCP is off: create the admin API with the MCP option")
)

// Admin is the admin API a mock server opts into, with ConfigBuilder.Admin of mockhttp or mockgrpc.
type Admin struct {
	opts adminserver.Options

	mu     sync.Mutex
	server *adminserver.Server // nil until it serves a mock server
}

// Option configures an Admin.
type Option func(a *Admin)

// Addr sets the address the admin API listens on: host:port, :port or a port. Empty, the default, is a random local
// port: the mock server's AdminURL returns where it listens.
func Addr(addr string) Option {
	return func(a *Admin) { a.opts.Addr = addr }
}

// MCP serves the admin operations over the Model Context Protocol too, for AI agents: Streamable HTTP at POST /mcp
// of the admin API, without sessions, and stdio with [Admin.ServeMCP]. Off, POST /mcp answers 501.
func MCP() Option {
	return func(a *Admin) { a.opts.MCP = true }
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

// Serve returns the admin API of the mock server of caps. Only mock servers call it, as they are created: code passes
// the Admin to the ConfigBuilder.Admin of mockhttp or mockgrpc. A second call fails with ErrServing.
func (a *Admin) Serve(caps adminapi.Capabilities) (adminapi.Server, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server != nil {
		return nil, ErrServing
	}

	s, err := adminserver.New(a.opts, caps)
	if err != nil {
		return nil, err
	}

	a.server = s

	return s, nil
}

// ServeMCP serves the admin operations over MCP on in and out, newline-delimited JSON-RPC, as an agent that starts
// the program talks to it over its stdin and stdout, until in ends or ctx is done. Nothing else may write to out, such
// as logs. It needs the MCP option, and the mock server created with the admin API; the mock server's Close waits for
// the calls it serves.
func (a *Admin) ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	if !a.opts.MCP {
		return errNoMCP
	}

	a.mu.Lock()
	s := a.server
	a.mu.Unlock()

	if s == nil {
		return errNotServing
	}

	return s.ServeMCP(ctx, in, out)
}

func checkAddr(addr string) error {
	if !httpx.ListenAddr(addr) {
		return fmt.Errorf("admin: address %q: expected host:port, :port or a port number", addr)
	}

	return nil
}
