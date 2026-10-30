package mockgrpc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// writeFile writes content into the file name of dir, and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// fileServer starts a server with the mock files of patterns.
func fileServer(t *testing.T, setup *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(Setup().Silent(), setup)
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

func TestFromFile_MocksEveryCallTypeFromFiles(t *testing.T) {
	// gRPC mocks had no file format: every one was Go code. A mock file describes the call it matches and the answer,
	// as HTTP mock files do, for each call type.
	m := fileServer(t, Setup().RootDir("testdata/mocks").MockFiles("*.yaml", "*.yml", "list.json"))
	c := pb.NewTestServiceClient(m.MustDial())

	var header, trailer metadata.MD
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
	res, err := c.Greetings(ctx, &pb.HiReq{Message: "hi"}, grpc.Header(&header), grpc.Trailer(&trailer))
	require.NoError(t, err)
	require.Equal(t, "hello", res.GetMessage())
	require.Equal(t, []string{"v"}, header.Get("k"))
	require.Equal(t, []string{"a", "b"}, trailer.Get("t"))

	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "denied"})
	st := status.Convert(err)
	require.Equal(t, codes.PermissionDenied, st.Code())
	require.Len(t, st.Details(), 1)

	info, ok := st.Details()[0].(*errdetails.ErrorInfo)
	require.True(t, ok, "%v", st.Details())
	require.Equal(t, "NOT_ALLOWED", info.GetReason())

	stream, err := c.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)

	var keys []string
	for {
		item, err := stream.Recv()
		if err != nil {
			require.Equal(t, codes.Aborted, status.Code(err))
			break
		}

		keys = append(keys, item.GetKey())
	}

	require.Equal(t, []string{"a", "b"}, keys)

	got, _ := upload(t, c, "a", "b")
	require.Equal(t, "2 items", got)

	answers, code := chat(t, c, nil, "hi", "what")
	require.Equal(t, codes.OK, code)
	require.Equal(t, []string{"hello", "?", "bye"}, answers)
}

func TestFromFile_ListsEveryProblem(t *testing.T) {
	// A mock file with problems says what each is, and where, as HTTP mock files do.
	path := writeFile(t, t.TempDir(), "bad.yaml", `
id: "no spaces"
request:
  message: {message: a}
  messages: [{message: a}]
response:
  message: {}
  messages: []
  status: {code: NOPE}
bidi: {}
`)

	m := fileServer(t, Setup())

	_, err := m.Mock(FromFile(path))
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)

	keys := make([]string, 0, len(cfgErr.Problems))
	for _, p := range cfgErr.Problems {
		keys = append(keys, p.Key)
	}

	require.ElementsMatch(t, []string{
		"id", "request.method", "request.messages", "bidi", "response.messages", "response.status.code",
	}, keys, "%v", err)
}

func TestFromFile_AMethodNoServiceHasFailsTheMock(t *testing.T) {
	path := writeFile(t, t.TempDir(), "typo.yaml", `{request: {method: Greet}, response: {}}`)
	m := fileServer(t, Setup())

	_, err := m.Mock(FromFile(path))
	require.ErrorContains(t, err, "unknown method")
}

func TestFromFile_AFileThatCantBeReadSaysWhy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "dir.yaml"), 0o755))

	m := fileServer(t, Setup())

	for _, tc := range []struct {
		path string
		code string
	}{
		{writeFile(t, dir, "mock.txt", "{}"), config.CodeUnsupportedFormat},
		{filepath.Join(dir, "missing.yaml"), config.CodeFileNotFound},
		{filepath.Join(dir, "dir.yaml"), config.CodeReadError},
	} {
		_, err := m.Mock(FromFile(tc.path))
		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "%v", err)
		require.Len(t, cfgErr.Problems, 1)
		require.Equal(t, tc.code, cfgErr.Problems[0].Code, "%s", tc.path)
	}
}

func TestFromBytes_ServesScenarios(t *testing.T) {
	scenariotest.Run(t, newScenarioServer(true))
}

func TestSingularMockBuilders_RejectMultipleYAMLDocuments(t *testing.T) {
	dir := t.TempDir()
	m := fileServer(t, Setup().InMemory())
	for _, tc := range []struct {
		name  string
		build func([]byte) (*Mock, error)
	}{
		{"FromFile", func(data []byte) (*Mock, error) {
			path := writeFile(t, dir, "greet.yaml", string(data))
			return FromFile(path).Build(m)
		}},
		{"FromBytes", func(data []byte) (*Mock, error) {
			return FromBytes(data, ".YML").Build(m)
		}},
		{"DecodeMock", func(data []byte) (*Mock, error) {
			return DecodeMock(m, data, "yaml")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := tc.build([]byte(`
request: {method: Greetings}
response: {message: {message: first}}
---
request: {method: Greetings}
response: {message: {message: second}}
`))
			require.Nil(t, mock)
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Len(t, cfgErr.Problems, 1)
			require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
			require.Equal(t, "expected one mock document, not 2", cfgErr.Problems[0].Message)
			require.Equal(t, "use MockFiles to load multiple mock documents", cfgErr.Problems[0].Hint)
		})
	}
}

func TestSingularMockBuilders_RejectMalformedLaterYAMLContent(t *testing.T) {
	dir := t.TempDir()
	m := fileServer(t, Setup().InMemory())
	for _, tc := range []struct {
		name  string
		build func([]byte) (*Mock, error)
	}{
		{"FromFile", func(data []byte) (*Mock, error) {
			path := writeFile(t, dir, "greet.yaml", string(data))
			return FromFile(path).Build(m)
		}},
		{"FromBytes", func(data []byte) (*Mock, error) {
			return FromBytes(data, "yaml").Build(m)
		}},
		{"DecodeMock", func(data []byte) (*Mock, error) {
			return DecodeMock(m, data, "yaml")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := tc.build([]byte("request: {method: Greetings}\nresponse: {}\n---\nrequest: ["))
			require.Nil(t, mock)
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Len(t, cfgErr.Problems, 1)
			require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
			require.ErrorContains(t, err, "document 2")
		})
	}
}

func TestFromBytes_EmptyDocumentsDoNotMakeOneMockPlural(t *testing.T) {
	m := fileServer(t, Setup().InMemory())
	m.MustMock(FromBytes([]byte(`
---
# empty first document
---
request: {method: Greetings}
response: {message: {message: hello}}
---
# empty last document
`), "yaml"))

	got, code := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hello", got)
}

func TestFromBytes_ConcatenatedJSONObjectsRemainInvalid(t *testing.T) {
	m := fileServer(t, Setup().InMemory())
	_, err := m.Mock(FromBytes([]byte(`{"request":{"method":"Greetings"},"response":{}} {"response":{}}`), "json"))
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Len(t, cfgErr.Problems, 1)
	require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
}
