// Package mcpwire holds the JSON-RPC 2.0 messages of the Model Context Protocol that MCP mocks read and write, over
// Streamable HTTP. It covers what an MCP server mock answers: the lifecycle, ping and tools.
package mcpwire

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"slices"
)

// Version is the JSON-RPC version of every message.
const Version = "2.0"

// _protocolVersions are the MCP versions an endpoint mock agrees to, newest first.
var _protocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// AgreeVersion returns the MCP version an endpoint mock answers a client that asks for version: version, when the mock
// knows it, and the newest it knows otherwise.
func AgreeVersion(version string) string {
	if slices.Contains(_protocolVersions, version) {
		return version
	}

	return _protocolVersions[0]
}

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
)

var (
	// ErrParse is the error of Decode for a body that isn't JSON.
	ErrParse = errors.New("mcpwire: the body is not JSON")

	// ErrInvalidRequest is the error of Decode for JSON that isn't one JSON-RPC 2.0 object, such as a batch.
	ErrInvalidRequest = errors.New("mcpwire: expected one JSON-RPC 2.0 object")
)

// _null is the id of a response to a message whose id is unknown.
var _null = jsontext.Value("null")

// Message is a JSON-RPC message a client sends: a request, a notification, or a response to the server.
//
// A Message never changes once Decode made it, so goroutines may share it.
type Message struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id,omitzero"`
	Method  string         `json:"method,omitzero"`
	Params  jsontext.Value `json:"params,omitzero"`

	call toolCall // the params of a tools/call, which Decode decodes once
}

// toolCall is the params of a tools/call: the tool's name, and its arguments as JSON, decoded when they are read.
type toolCall struct {
	Name      string                    `json:"name"`
	Arguments map[string]jsontext.Value `json:"arguments"`
}

// Decode decodes one JSON-RPC message. It fails with ErrParse for a body that isn't JSON, and with ErrInvalidRequest for
// JSON that isn't one object with "jsonrpc": "2.0".
func Decode(body []byte) (*Message, error) {
	var m Message

	err := json.Unmarshal(body, &m)

	switch {
	case err == nil && m.JSONRPC == Version:
		if m.IsToolCall() {
			m.call = decodeToolCall(m.Params)
		}

		return &m, nil
	case err != nil:
		if _, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
			return nil, ErrParse
		}
	}

	return nil, ErrInvalidRequest
}

// IsRequest reports a message with a method and an id, which takes an answer.
func (m *Message) IsRequest() bool {
	return m.Method != "" && len(m.ID) > 0
}

// IsToolCall reports a tools/call request.
func (m *Message) IsToolCall() bool {
	return m.IsRequest() && m.Method == "tools/call"
}

// ToolName returns the name of the tool a tools/call calls, or "" when its params don't name one.
func (m *Message) ToolName() string {
	return m.call.Name
}

// Argument returns the argument name of a tools/call, decoded anew on each call: numbers are float64, objects
// map[string]any and lists []any. It is nil when the call hasn't the argument.
func (m *Message) Argument(name string) any {
	raw, ok := m.call.Arguments[name]
	if !ok {
		return nil
	}

	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}

	return v
}

// decodeToolCall decodes params, those of a tools/call. Arguments that aren't an object leave the name: only the
// arguments are then missing.
func decodeToolCall(params jsontext.Value) toolCall {
	var call toolCall
	if len(params) == 0 || json.Unmarshal(params, &call) == nil {
		return call
	}

	var named struct {
		Name string `json:"name"`
	}

	_ = json.Unmarshal(params, &named)

	return toolCall{Name: named.Name}
}

// InitializeVersion returns the protocolVersion of the params of an initialize, or "" when there is none.
func (m *Message) InitializeVersion() string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}

	if len(m.Params) > 0 {
		_ = json.Unmarshal(m.Params, &p)
	}

	return p.ProtocolVersion
}

// Response is the answer to a request: a result or an error.
type Response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  any            `json:"result,omitzero"`
	Error   *Error         `json:"error,omitzero"`
}

// Error is the error of a Response.
type Error struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitzero"`
}

// Implementation names a server.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the result of an initialize.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      Implementation `json:"serverInfo"`
}

// Tool is a tool of a server, as tools/list lists it.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitzero"`
	InputSchema jsontext.Value `json:"inputSchema"`
}

// ListToolsResult is the result of a tools/list.
type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

// Content is a content of a tool's result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult is the result of a tools/call.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitzero"`
}

// Result returns the response with result to the request with id. An empty id is written as null.
func Result(id jsontext.Value, result any) Response {
	return Response{JSONRPC: Version, ID: orNull(id), Result: result}
}

// Fail returns the response with an error to the request with id. An empty id is written as null, and a nil data
// is left out.
func Fail(id jsontext.Value, code int64, message string, data any) Response {
	return Response{JSONRPC: Version, ID: orNull(id), Error: &Error{Code: code, Message: message, Data: data}}
}

// Marshal encodes v as JSON, with the keys of maps in order, so equal values give equal bytes.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v, json.Deterministic(true))
}

// orNull returns id, or null when it is empty.
func orNull(id jsontext.Value) jsontext.Value {
	if len(id) == 0 {
		return _null
	}

	return id
}
