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
	"net/http"
	"regexp"
	"sync"

	adminserver "github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

var _ adminapi.API = (*Admin)(nil)

// _version matches a version of an npm package: major.minor.patch, with a pre-release such as -beta.1 or not.
var _version = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

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

// DocsVersion sets the version of Scalar, the npm package @scalar/api-reference, that the documentation page of the
// admin API, at / and /docs, loads from jsDelivr, such as 1.80.0: a newer one than vlatte pins, before vlatte ships
// it. Empty, the default, is the version vlatte pins. The page checks the pinned version against the hash of its
// file; it loads another unchecked, as its hash isn't known.
func DocsVersion(version string) Option {
	return func(a *Admin) { a.opts.DocsVersion = version }
}

// Use adds middlewares that wrap every HTTP request of the admin API, such as one that checks an authentication header.
// They run in the order they are added, across calls, before the admin API routes the request: one that answers
// without calling the next handler answers in place of the admin API. Requests that come once the mock server closes
// get a 503 without reaching them. MCP over stdio, with [Admin.ServeMCP], doesn't go through them.
func Use(middlewares ...func(http.Handler) http.Handler) Option {
	return func(a *Admin) { a.opts.Middlewares = append(a.opts.Middlewares, middlewares...) }
}

// New creates an admin API. It fails on an address that is neither host:port, :port nor a port, and on a docs version
// that isn't one, such as 1.80.0.
func New(options ...Option) (*Admin, error) {
	a := &Admin{}
	for _, option := range options {
		option(a)
	}

	if err := checkAddr(a.opts.Addr); err != nil {
		return nil, err
	}

	if v := a.opts.DocsVersion; v != "" && !_version.MatchString(v) {
		return nil, fmt.Errorf("admin: docs version %q: expected a version such as 1.80.0", v)
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
