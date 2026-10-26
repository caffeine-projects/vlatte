package mockgrpc

import (
	"context"
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

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
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

func TestMockFiles_LoadAtStartAndOnReload(t *testing.T) {
	dir := t.TempDir()
	file := writeFile(t, dir, "greet.yaml", `{request: {method: Greetings}, response: {message: {message: v1}}}`)

	m := fileServer(t, Setup().RootDir(dir).MockFiles("*.yaml"))
	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "v1", got)

	writeFile(t, dir, "greet.yaml", `{request: {method: Greetings}, response: {message: {message: v2}}}`)
	require.NoError(t, m.Reload())

	got, _ = greetWith(t, c, "hi")
	require.Equal(t, "v2", got, "a reload swaps the mocks of the files")

	writeFile(t, dir, "greet.yaml", `{request: {method: Nope}, response: {}}`)
	require.Error(t, m.Reload())

	got, _ = greetWith(t, c, "hi")
	require.Equal(t, "v2", got, "a reload that fails changes nothing")

	require.NoError(t, os.Remove(file))
	require.NoError(t, m.Reload())

	_, code := greetWith(t, c, "hi")
	require.Equal(t, codes.NotFound, code, "a removed file's mock goes")
}

func TestMockFiles_AStartThatFailsToLoadThemServesNothing(t *testing.T) {
	path := writeFile(t, t.TempDir(), "bad.yaml", `{request: {method: Nope}, response: {}}`)
	m := MustNew(Setup().Silent().MockFiles(path))

	require.ErrorContains(t, m.Start(), "unknown method")
	require.Empty(t, m.Addr(), "nothing listens")
}

func TestDescribe_AMockBuiltInGoLoadsBackTheSame(t *testing.T) {
	// The admin API returns and saves mocks as their descriptions: one built in Go, of what mock files hold, loads
	// back as a mock that answers the same.
	builders := []vlatte.Builder[*Mock, *Server]{
		Method("Greetings").ID("a").Header("x-team", match.Equal("core")).JSON(`{"message": "a"}`).
			Reply(Message(&pb.HiRes{Message: "for a"}).Header("h", "1")),
		Method("Greetings").ID("b").Priority(1).Message(&pb.HiReq{Message: "b"}).
			Reply(Status(codes.NotFound, "no b").Details(&errdetails.ErrorInfo{Reason: "B"})),
		Method("List").ID("c").Reply(JSON(`{"key": "x"}`, `{"key": "y"}`).Status(codes.Aborted, "cut")),
	}

	origin := fileServer(t, Setup())
	scope := origin.MustMock(builders...)

	loaded := fileServer(t, Setup())

	for _, mock := range scope.Mocks() {
		doc, err := mock.Describe().MarshalJSON()
		require.NoError(t, err)

		_, err = loaded.Mock(FromBytes(doc, "json"))
		require.NoError(t, err, "%s", doc)
	}

	for _, m := range []*Server{origin, loaded} {
		c := pb.NewTestServiceClient(m.MustDial())

		ctx := metadata.AppendToOutgoingContext(t.Context(), "x-team", "core")
		res, err := c.Greetings(ctx, &pb.HiReq{Message: "a"})
		require.NoError(t, err)
		require.Equal(t, "for a", res.GetMessage())

		_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "b"})
		st := status.Convert(err)
		require.Equal(t, codes.NotFound, st.Code())
		require.Len(t, st.Details(), 1)

		stream, err := c.List(context.WithoutCancel(t.Context()), &pb.ListReq{})
		require.NoError(t, err)

		item, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "x", item.GetKey())
	}
}
