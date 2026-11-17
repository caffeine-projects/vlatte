package mockhttp

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mcpwire"
)

var (
	errMCPMockKind      = errors.New("mock: mcp: set MCPServerInfo for the endpoint mock or MCPTool for a tool mock")
	errMCPRequestLine   = errors.New("mock: mcp: an MCP mock matches its own method and path")
	errMCPEndpointReply = errors.New("mock: mcp: the endpoint mock answers on its own: remove Reply")
	errMCPNoBody        = errors.New("mock: mcp: the server parses no request bodies, which MCP mocks read: remove NoBodyParsing")
	errMCPNotStarted    = errors.New("mcp: start the mock with MCP(path)")
	errMCPKindSet       = errors.New("mcp: the mock is already an endpoint or a tool: start another with MCP(path)")
	errMCPNoTool        = errors.New("mcp: an input schema or an argument belongs to a tool: call MCPTool first")
)

// _mcpArgumentKey prefixes the key of the matcher of a tool's argument.
const _mcpArgumentKey = "arguments."

// _mcpDefaultInputSchema is the input schema tools/list shows for a tool mock without one.
var _mcpDefaultInputSchema = jsontext.Value(`{"type":"object"}`)

// mcpSpec is what makes a mock an MCP mock: its endpoint, and the server it is the endpoint of, or the tool it answers.
type mcpSpec struct {
	endpoint string
	server   *mcpwire.Implementation
	tool     *mcpTool
}

// mcpTool is the tool a tool mock answers, as tools/list shows it.
type mcpTool struct {
	name        string
	description string
	inputSchema jsontext.Value // nil shows {"type":"object"}
}

// clone returns a copy of s that shares nothing a builder changes.
func (s *mcpSpec) clone() *mcpSpec {
	c := *s

	if s.server != nil {
		server := *s.server
		c.server = &server
	}

	if s.tool != nil {
		tool := *s.tool
		c.tool = &tool
	}

	return &c
}

// describe returns the mcp key of a mock file. The arguments of a tool come from expectations, the matchers of the
// mock, described as the headers of a mock are.
func (s *mcpSpec) describe(expectations []*expectation) map[string]any {
	desc := map[string]any{"endpoint": s.endpoint}

	if s.server != nil {
		desc["server"] = map[string]any{"name": s.server.Name, "version": s.server.Version}
		return desc
	}

	tool := map[string]any{"name": s.tool.name}

	if s.tool.description != "" {
		tool["description"] = s.tool.description
	}

	// As JSON, so its numbers keep their value.
	if len(s.tool.inputSchema) > 0 {
		tool["input_schema"] = s.tool.inputSchema
	}

	var args []*expectation
	for _, e := range expectations {
		if matchingScope(e.Target) == matchingScopeMCPCall && strings.HasPrefix(e.Key, _mcpArgumentKey) {
			args = append(args, e)
		}
	}

	if described := describeKeyed(args, func(e *expectation) string {
		return strings.TrimPrefix(e.Key, _mcpArgumentKey)
	}); len(described) > 0 {
		tool["arguments"] = described
	}

	desc["tool"] = tool

	return desc
}

// MCP starts a mock of an MCP server at path, over Streamable HTTP. It matches requests to exactly path: /mcp is
// neither /mcp/ nor /mcp/v2. MCPServerInfo then makes it the endpoint mock of that server, which answers its lifecycle
// and tools/list, and MCPTool the mock of one of its tools.
//
// An MCP mock matches its own method and path, so building it fails when Method, URL, URLPath, URLPathTemplate or
// PathParam is set, and on a server with NoBodyParsing, as MCP mocks read the body. The other methods of Builder, such as
// Header, Times, a scenario or Delay, work as on any mock, so REST mocks and several MCP servers, each on its own
// path, can share a server.
func MCP(path string) *Builder {
	return Request().mcpAt(path)
}

// mcpAt makes b an MCP mock at path, which MCPServerInfo or MCPTool then make the endpoint mock or a tool mock.
func (b *Builder) mcpAt(path string) *Builder {
	if !strings.HasPrefix(path, "/") {
		b.setErr(fmt.Errorf("mcp: endpoint %q: expected a path that starts with /", path))
	}

	b.spec.mcp = &mcpSpec{endpoint: path}
	b.appendExpectation(mcpPathPart(path))

	return b
}

// mcpPathPart returns the part of an MCP mock that matches requests to its path, which reads no body.
func mcpPathPart(path string) *expectation {
	return &expectation{
		Target:   int(matchingScopeMCP),
		Key:      "endpoint",
		Selector: selectURLPath,
		Matcher:  match.Equal(path),
	}
}

// MCPServerInfo makes the mock the endpoint mock of its MCP server, named name, at version. It answers every request
// to its path but a tool call: initialize, ping and notifications; tools/list, with the tools of the server's enabled
// tool mocks of the path; and other methods with JSON-RPC errors. A request with another HTTP method than POST gets 405.
//
// A tool call that no tool mock matches is answered by the server, as any request no mock matches: on the path of an
// endpoint mock, with a JSON-RPC error, -32602, whose data lists the near misses. A proxy_all of the server forwards it
// instead.
//
// The endpoint mock takes no Reply. Keep one endpoint mock per path: of two, the first in match order answers, and two
// in mock files fail to load.
func (b *Builder) MCPServerInfo(name, version string) *Builder {
	if !b.mcpKindFree() {
		return b
	}

	b.spec.mcp.server = &mcpwire.Implementation{Name: name, Version: version}

	// The path check MCP added becomes one part: the path, and not a tool call. A tool call to the path then passes no
	// part of the endpoint mock, which the near misses of a tool call no tool mock matched leave out.
	i := slices.IndexFunc(b.spec.expectations, func(e *expectation) bool {
		return matchingScope(e.Target) == matchingScopeMCP && e.Key == "endpoint"
	})
	b.spec.expectations[i] = &expectation{
		Target:   int(matchingScopeMCPEndpoint),
		Key:      "endpoint",
		Selector: selectEndpointRequest(b.spec.mcp.endpoint),
		Matcher:  vlatte.MatcherFunc("MCPEndpoint", matchEndpointRequest),
	}

	return b
}

// MCPTool makes the mock answer the calls of the tool name: a POST of tools/call. description is what tools/list
// shows of it. The mock needs a Reply, such as MCPJSON, or any other reply, such as a ProxyReply that forwards the call.
func (b *Builder) MCPTool(name, description string) *Builder {
	if !b.mcpKindFree() {
		return b
	}

	b.spec.mcp.tool = &mcpTool{name: name, description: description}
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeMCP),
		Key:      "method",
		Selector: selectMethod,
		Matcher:  match.Equal(http.MethodPost),
	})
	b.appendExpectation(&expectation{
		Target:   int(matchingScopeMCPCall),
		Key:      "tool",
		Selector: selectToolName,
		Matcher:  match.Equal(name),
	})

	return b
}

// MCPInputSchema sets the JSON Schema of the arguments of the tool, which tools/list shows. schema is encoded to JSON
// now, and must be an object; without one, tools/list shows {"type":"object"}.
func (b *Builder) MCPInputSchema(schema any) *Builder {
	if !b.mcpToolSet() {
		return b
	}

	raw, err := mcpwire.Marshal(schema)

	switch {
	case err != nil:
		b.setErr(fmt.Errorf("mcp: input schema: %w", err))
	case jsontext.Value(raw).Kind() != '{':
		b.setErr(fmt.Errorf("mcp: input schema: expected a JSON object, not %s", raw))
	default:
		b.spec.mcp.tool.inputSchema = raw
	}

	return b
}

// MCPArgument matches the argument name of the calls of the tool with m. An argument the call doesn't have is nil to
// m. m gets a whole number that fits as an int64 and another number as a float64, an object as a map[string]any and a
// list as a []any. Numbers compare by value, so match.Equal(42) matches the 42 of a call.
func (b *Builder) MCPArgument(name string, m vlatte.Matcher) *Builder {
	if !b.mcpToolSet() {
		return b
	}

	b.appendExpectation(&expectation{
		Target:   int(matchingScopeMCPCall),
		Key:      _mcpArgumentKey + name,
		Selector: selectArgument(name),
		Matcher:  m,
	})

	return b
}

// mcpKindFree reports whether b is an MCP mock that is neither the endpoint mock nor a tool mock yet, and records why
// not otherwise.
func (b *Builder) mcpKindFree() bool {
	switch {
	case b.spec.mcp == nil:
		b.setErr(errMCPNotStarted)
		return false
	case b.spec.mcp.server != nil || b.spec.mcp.tool != nil:
		b.setErr(errMCPKindSet)
		return false
	}

	return true
}

// mcpToolSet reports whether b is a tool mock, and records why not otherwise.
func (b *Builder) mcpToolSet() bool {
	switch {
	case b.spec.mcp == nil:
		b.setErr(errMCPNotStarted)
		return false
	case b.spec.mcp.tool == nil:
		b.setErr(errMCPNoTool)
		return false
	}

	return true
}

// setErr records err for Build to return, unless an error is recorded already: the first one wins.
func (b *Builder) setErr(err error) {
	if b.err == nil {
		b.err = err
	}
}

// mcpReply checks the MCP mock b for app, and returns what it replies with: the endpoint's own reply, or the Reply of
// a tool mock.
func (b *Builder) mcpReply(app *Server) (ReplierFactory, error) {
	s := b.spec.mcp

	if s.server == nil && s.tool == nil {
		return nil, errMCPMockKind
	}

	setters := map[matchingScope]string{
		matchingScopeMethod:          "Method",
		matchingScopeURL:             "URL",
		matchingScopeURLPath:         "URLPath",
		matchingScopeURLPathTemplate: "URLPathTemplate",
	}

	for _, e := range b.spec.expectations {
		if setter, ok := setters[matchingScope(e.Target)]; ok {
			return nil, fmt.Errorf("%w: remove %s", errMCPRequestLine, setter)
		}
	}

	if len(b.pathParams) > 0 {
		return nil, fmt.Errorf("%w: remove PathParam", errMCPRequestLine)
	}

	if app.config.NoBodyParsing {
		return nil, errMCPNoBody
	}

	if s.server == nil {
		return b.reply, nil
	}

	if b.reply != nil {
		return nil, errMCPEndpointReply
	}

	return &mcpEndpointReply{endpoint: s.endpoint, server: *s.server}, nil
}

// mcpToolCall decodes the body of the request in as a JSON-RPC message, and reports whether it is a tools/call.
func mcpToolCall(in *selectorInput) (*mcpwire.Message, bool) {
	m, err := mcpMessageOf(in.values)

	return m, err == nil && m.IsToolCall()
}

// selectEndpointRequest selects whether the request is one the endpoint mock at path answers: a request to path, but a
// tool call. It reads no body for a request to another path, and selects journal.ErrBodyCut for a POST to path of a
// journal entry whose body was cut.
func selectEndpointRequest(path string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, in *selectorInput) any {
		if in.URL.Path != path {
			return false
		}

		if in.RawRequest.Method != http.MethodPost {
			return true
		}

		// A body the journal cut can't tell a tool call apart.
		if in.bodyTruncated {
			return journal.ErrBodyCut
		}

		_, ok := mcpToolCall(in)

		return !ok
	}
}

// matchEndpointRequest matches what selectEndpointRequest selects: true for a request of the endpoint mock, and an
// error for a request whose cut body can't tell.
func matchEndpointRequest(arg vlatte.MatcherArg) (bool, error) {
	if err, ok := arg.Value().(error); ok {
		return false, err
	}

	ok, _ := arg.Value().(bool)

	return ok, nil
}

// selectToolName selects the name of the tool a tool call calls, or nil for another request.
func selectToolName(_ context.Context, in *selectorInput) any {
	m, ok := mcpToolCall(in)
	if !ok {
		return nil
	}

	return m.ToolName()
}

// selectArgument selects the argument name of a tool call, or nil for another request and an argument it hasn't.
func selectArgument(name string) vlatte.Selector[*selectorInput] {
	return func(_ context.Context, in *selectorInput) any {
		m, ok := mcpToolCall(in)
		if !ok {
			return nil
		}

		return m.Argument(name)
	}
}

// isMCPEndpoint reports whether m is an enabled endpoint mock of an MCP server, and its path.
func isMCPEndpoint(m *Mock) (string, bool) {
	s := m.spec.mcp
	if s == nil || s.server == nil || !m.Enabled() {
		return "", false
	}

	return s.endpoint, true
}

// mcpDecoded is the body of a request decoded as a JSON-RPC message, or why it isn't one. It never changes once made.
type mcpDecoded struct {
	msg *mcpwire.Message
	err error
}

// mcpMessageOf decodes the body of rv as a JSON-RPC message, once: the request's matching, its reply and its no-match
// answer read the same message. Should two goroutines read it at once, each decodes the same bytes, the first stored
// wins, and both get it.
func mcpMessageOf(rv *RequestValues) (*mcpwire.Message, error) {
	d := rv.mcp.Load()
	if d == nil {
		m, err := mcpwire.Decode(rv.RawBodyBytes)
		rv.mcp.CompareAndSwap(nil, &mcpDecoded{msg: m, err: err})
		d = rv.mcp.Load()
	}

	return d.msg, d.err
}
