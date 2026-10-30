package handler_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/buildinfo"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestNewMCP_ToolsAreTheOperationsTheServerCanDo(t *testing.T) {
	// An agent sees only the tools it can call: a tool that would answer that the server can't do it would cost the
	// agent a call, and its context, to learn nothing.
	always := []string{"get_mock", "health", "list_mocks", "remove_all_mocks", "remove_mock", "reset", "update_mock"}

	tests := []struct {
		name  string
		caps  adminapi.Capabilities
		build bool
		want  []string
	}{
		{name: "the least a server has", want: always},
		{
			// A gRPC server: mocks of code only, and a journal.
			name: "a journal",
			caps: adminapi.Capabilities{Journal: journalOf(t)},
			want: append([]string{"clear_requests", "get_request", "list_requests", "query_requests", "verify_requests"}, always...),
		},
		{
			// A store kept in memory alone has no file to reload.
			name: "a key-value store in memory",
			caps: adminapi.Capabilities{KV: vlatte.NewMemoryKV()},
			want: append([]string{"delete_kv", "get_kv", "list_kv", "set_kv"}, always...),
		},
		{
			name: "everything",
			caps: adminapi.Capabilities{
				Files: &fakeFiles{}, Recorder: &fakeRecorder{}, Scenarios: newScenarios(), Journal: journalOf(t),
				MockSchema: []byte(`{}`), KV: vlatte.MustNewCSVKV(writeKVFile(t, "key,value\n")),
			},
			build: true,
			want: append([]string{
				"add_mock", "clear_requests", "delete_kv", "get_kv", "get_request", "list_kv", "list_requests", "list_scenarios",
				"mock_schema", "query_requests", "recording_status", "reload_kv", "reload_mocks", "replace_mock", "reset_scenario",
				"reset_scenarios", "save_mocks", "set_kv", "set_scenario", "start_recording", "stop_recording", "validate_mock",
				"verify_requests",
			}, always...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := tt.caps
			caps.Mocks = adminapi.MocksOf[*fakeMock](newStore(t), nil)
			if tt.build {
				caps.Mocks = adminapi.MocksOf[*fakeMock](newStore(t), &fakeBuilder{})
			}

			cs := connectInMemory(t, handler.NewMCP(handler.NewOperations(caps)))

			res, err := cs.ListTools(t.Context(), nil)
			require.NoError(t, err)

			names := make([]string, 0, len(res.Tools))
			for _, tool := range res.Tools {
				names = append(names, tool.Name)
				require.NotEmpty(t, tool.Description, tool.Name)
			}

			slices.Sort(names)
			slices.Sort(tt.want)
			require.Equal(t, tt.want, names)
		})
	}
}

func TestNewMCP_ReadOnlyToolsSayTheyAre(t *testing.T) {
	// A client asks before it runs a tool that changes the server, and runs the others: the hints decide which.
	caps := adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), &fakeBuilder{}), Journal: journalOf(t)}
	cs := connectInMemory(t, handler.NewMCP(handler.NewOperations(caps)))

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)

	readOnly := make(map[string]bool)
	for _, tool := range res.Tools {
		readOnly[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	}

	require.True(t, readOnly["list_mocks"])
	require.True(t, readOnly["query_requests"])
	require.True(t, readOnly["verify_requests"])
	require.False(t, readOnly["add_mock"])
	require.False(t, readOnly["reset"])
}

func TestNewMCP_ArgumentsOutOfTheSchemaAreRefused(t *testing.T) {
	// A tool's arguments are checked against its input schema before the operation runs, and the error says what to
	// fix: a call without the mock changes nothing.
	store := newStore(t, newFake("hello"))
	cs := connect(t, store)

	text, failed := callTool(t, cs, "replace_mock", map[string]any{"id": "hello"})
	require.True(t, failed)
	require.Contains(t, text, `missing properties: ["mock"]`)

	text, failed = callTool(t, cs, "add_mock", map[string]any{"mock": "hello"})
	require.True(t, failed, text)
	require.Equal(t, []string{"hello"}, ids(t, store))
}

func TestNewMCP_ReportsTheVersionTheCLIPrints(t *testing.T) {
	// An MCP client lists the server by the version it reports: the one vlatte version prints for the same build,
	// devel included.
	cs := connect(t, newStore(t))

	require.Equal(t, buildinfo.Version(), cs.InitializeResult().ServerInfo.Version)
}

func TestMCPHandler_ServesOverStreamableHTTP(t *testing.T) {
	// An agent that runs elsewhere reaches the tools at POST /mcp, with no session to keep.
	cs := connect(t, newStore(t, newFake("hello")))

	doc := conformsAs(t, call(t, cs, "list_mocks", nil), "json", "MockList")
	require.Equal(t, []string{"hello"}, mockIDs(doc))

	callFails(t, cs, "get_mock", map[string]any{"id": "missing"}, http.StatusNotFound, "mock_not_found")
}

// connectInMemory connects an MCP client to s, in memory.
func connectInMemory(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()

	serverT, clientT := mcp.NewInMemoryTransports()

	ss, err := s.Connect(context.Background(), serverT, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(t.Context(), clientT, nil)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})

	return cs
}

func TestAddTool_LogsTheErrorsTheAgentCantFix(t *testing.T) {
	// An internal error, such as a store that is down, is the server's to fix: it reaches the server's log, as a
	// request's does, and not only the agent.
	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), pickAllErr: errors.New("store: down")}
	cs := connect(t, store, adminapi.Capabilities{Logger: &logger})

	callFails(t, cs, "list_mocks", nil, http.StatusInternalServerError, "internal")
	require.JSONEq(t, `{"level": "error", "code": "internal", "tool": "list_mocks", "message": "admin: store: down"}`, logs.String())

	logs.Reset()
	callFails(t, cs, "get_mock", map[string]any{"id": "missing"}, http.StatusNotFound, "mock_not_found")
	require.Empty(t, logs.String(), "the agent fixes what it asked for")
}

func TestNewMCP_DocumentArgumentsAreObjectsWithTheirDescription(t *testing.T) {
	// An agent writes the arguments from the input schema: a mock is an object, and its description says where its
	// schema is.
	cs := connect(t, newStore(t))

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)

	i := slices.IndexFunc(res.Tools, func(tool *mcp.Tool) bool { return tool.Name == "add_mock" })
	require.NotEqual(t, -1, i)

	schema, err := json.Marshal(res.Tools[i].InputSchema)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"type": "object",
		"required": ["mock"],
		"properties": {"mock": {"type": "object", "description": "The mock, as a mock file writes it: see mock_schema."}},
		"additionalProperties": false
	}`, string(schema))
}

func TestAddTool_AProblemThatCantBeEncodedStillFails(t *testing.T) {
	// A problem whose origin isn't UTF-8, such as a mock file's path read from a disk, can't be a JSON problem
	// document: the tool still fails, with the problem's detail, so the agent learns what failed.
	err := &config.Error{Scope: "mock", Problems: []config.Problem{{
		Code: config.CodeInvalidValue, Origin: "file \xff.yaml", Key: "response.status", Message: "bad status",
	}}}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: &fakeFiles{err: err}})

	text, failed := callTool(t, cs, "reload_mocks", nil)
	require.True(t, failed)
	require.Equal(t, "the mock files have 1 problem", text)
}
