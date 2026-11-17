package mcpwire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecode_KeepsTheIDAsSentAndTellsMessagesApart(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantErr  error
		id       string
		request  bool
		toolCall bool
	}{
		{
			name:    "a numeric id stays as sent",
			body:    `{"jsonrpc":"2.0","id":7,"method":"ping"}`,
			id:      "7",
			request: true,
		},
		{
			name:    "a string id stays as sent",
			body:    `{"jsonrpc":"2.0","id":"a-1","method":"ping"}`,
			id:      `"a-1"`,
			request: true,
		},
		{
			name: "a notification has no id: it is no request",
			body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		},
		{
			name: "a client's response has no method",
			body: `{"jsonrpc":"2.0","id":3,"result":{}}`,
			id:   "3",
		},
		{
			name:     "a tool call",
			body:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_me"}}`,
			id:       "1",
			request:  true,
			toolCall: true,
		},
		{
			name:    "a batch is not one object",
			body:    `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
			wantErr: ErrInvalidRequest,
		},
		{
			name:    "another JSON-RPC version",
			body:    `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
			wantErr: ErrInvalidRequest,
		},
		{
			name:    "a body that isn't JSON",
			body:    `not json`,
			wantErr: ErrParse,
		},
		{
			name:    "an empty body",
			body:    ``,
			wantErr: ErrParse,
		},
		{
			name:    "a string",
			body:    `"ping"`,
			wantErr: ErrInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode([]byte(tt.body))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.id, string(m.ID))
			require.Equal(t, tt.request, m.IsRequest())
			require.Equal(t, tt.toolCall, m.IsToolCall())
		})
	}
}

func TestFail_AnUnknownIDIsNull(t *testing.T) {
	// The answer to a message whose id is unknown, such as one that isn't JSON, names no request.
	b, err := Marshal(Fail(nil, CodeMethodNotFound, "m", nil))
	require.NoError(t, err)
	require.Equal(t, `{"jsonrpc":"2.0","id":null,"error":{"code":-32601,"message":"m"}}`, string(b))
}

func TestResult_EchoesTheIDWithKeysInOrder(t *testing.T) {
	b, err := Marshal(Result([]byte(`"x"`), map[string]any{"b": 2, "a": 1}))
	require.NoError(t, err)
	require.Equal(t, `{"jsonrpc":"2.0","id":"x","result":{"a":1,"b":2}}`, string(b))
}

func TestToolNameAndArgument_ReadTheCallAsSentAndNeverChange(t *testing.T) {
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"issue_read",` +
		`"arguments":{"owner":"octocat","issue_number":42,"labels":["bug"]}}}`))
	require.NoError(t, err)

	require.Equal(t, "issue_read", m.ToolName())
	require.Equal(t, "octocat", m.Argument("owner"))

	// The message never changes: reading again gives equal answers, and each Argument is a value of its own.
	require.Equal(t, "issue_read", m.ToolName())
	require.Equal(t, []any{"bug"}, m.Argument("labels"))
	m.Argument("labels").([]any)[0] = "changed"
	require.Equal(t, []any{"bug"}, m.Argument("labels"))
	require.Equal(t, int64(42), m.Argument("issue_number"))
	require.Equal(t, []any{"bug"}, m.Argument("labels"))
	require.Nil(t, m.Argument("missing"))
}

func TestArgument_KeepsEveryDigitOfAWholeNumber(t *testing.T) {
	// A whole number past 2^53 would change as a float64: it stays an int64, nested ones too.
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"t",` +
		`"arguments":{"id":9007199254740993,"nested":{"ids":[9007199254740993]},"ratio":1.5}}}`))
	require.NoError(t, err)

	require.Equal(t, int64(9007199254740993), m.Argument("id"))
	require.Equal(t, map[string]any{"ids": []any{int64(9007199254740993)}}, m.Argument("nested"))
	require.Equal(t, 1.5, m.Argument("ratio"))
}

func TestToolNameAndArgument_ParamsThatDontDecodeAreZero(t *testing.T) {
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":[1]}`))
	require.NoError(t, err)
	require.Empty(t, m.ToolName())
	require.Nil(t, m.Argument("a"))
}

func TestInitializeVersion_IsEmptyWithoutAVersion(t *testing.T) {
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`))
	require.NoError(t, err)
	require.Equal(t, "2025-06-18", m.InitializeVersion())

	m, err = Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	require.NoError(t, err)
	require.Empty(t, m.InitializeVersion())
}

func TestToolName_ArgumentsThatArentAnObjectKeepTheName(t *testing.T) {
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"t","arguments":5}}`))
	require.NoError(t, err)
	require.Equal(t, "t", m.ToolName())
	require.Nil(t, m.Argument("a"))
}

func TestToolName_OfAnotherMethodIsEmpty(t *testing.T) {
	m, err := Decode([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"name":"t"}}`))
	require.NoError(t, err)
	require.Empty(t, m.ToolName())
}

func TestAgreeVersion_KnownVersionsAndTheNewestOtherwise(t *testing.T) {
	// A client checks the version the server agrees to: an unknown one gets the newest the mock knows.
	require.Equal(t, "2025-06-18", AgreeVersion("2025-06-18"))
	require.Equal(t, "2024-11-05", AgreeVersion("2024-11-05"))
	require.Equal(t, "2025-11-25", AgreeVersion("2026-07-28"))
	require.Equal(t, "2025-11-25", AgreeVersion(""))
}
