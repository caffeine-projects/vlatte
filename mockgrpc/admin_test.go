package mockgrpc

import (
	"bufio"
	json "encoding/json/v2"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// adminServer starts a gRPC mock server with its admin API, and a client of it.
func adminServer(t *testing.T) (*Server, pb.TestServiceClient) {
	t.Helper()

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger))
	pb.RegisterTestServiceServer(m, pb.UnimplementedTestServiceServer{})
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return m, pb.NewTestServiceClient(conn)
}

// call sends a request to the admin API and returns its status and body.
func call(t *testing.T, method, url, body string) (int, string) {
	t.Helper()

	r, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode, string(b)
}

func TestAdmin_IsTheSameForGRPC(t *testing.T) {
	// The admin API of a gRPC mock server is the HTTP one, on its own address: it lists the mocks with their hits,
	// and turns them off and on.
	m, client := adminServer(t)
	scope := m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "bye"})))
	id := scope.IDs()[0]

	_, err := client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	status, body := call(t, http.MethodGet, m.AdminURL()+"/health", "")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"status": "ok", "kind": "grpc", "url": "`+m.Addr()+`"}`, body)

	_, body = call(t, http.MethodGet, m.AdminURL()+"/mocks", "")
	require.JSONEq(t, `{"mocks": [{"id": "`+id+`", "priority": 0, "enabled": true, "hits": 1}]}`, body)

	status, body = call(t, http.MethodPatch, m.AdminURL()+"/mocks/"+id, `{"enabled": false}`)
	require.Equal(t, http.StatusOK, status, body)

	_, err = client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, statusCode(err), "a disabled mock matches no call")

	status, _ = call(t, http.MethodDelete, m.AdminURL()+"/mocks/"+id, "")
	require.Equal(t, http.StatusNoContent, status)

	mocks, err := m.storage.PickAll(t.Context())
	require.NoError(t, err)
	require.Empty(t, mocks)
}

func TestAdmin_WhatGRPCMocksCantDo(t *testing.T) {
	// gRPC mocks have no mock file format, recordings or scenarios: those operations answer 501, and say so.
	m, _ := adminServer(t)
	scope := m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "bye"})))

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/mocks"},
		{method: http.MethodGet, path: "/mocks/" + scope.IDs()[0]},
		{method: http.MethodPost, path: "/mocks/reload"},
		{method: http.MethodPost, path: "/recording"},
		{method: http.MethodGet, path: "/scenarios"},
	} {
		status, body := call(t, route.method, m.AdminURL()+route.path, `{}`)
		require.Equal(t, http.StatusNotImplemented, status, route.path)
		require.Contains(t, body, `"code": "not_supported"`)
	}
}

func TestAdmin_StreamsTheCalls(t *testing.T) {
	// A client follows the calls the mocks answer, and those none matched, as HTTP mock servers' clients do.
	m, client := adminServer(t)
	m.MustMock(UnaryMethod("Greetings").Reply(Unary().Message(&pb.HiRes{Message: "bye"})))

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.AdminURL()+"/events", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	_, err = client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	stream, err := client.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, statusCode(err))

	lines := bufio.NewScanner(res.Body)
	byType := make(map[string]map[string]any)

	for len(byType) < 2 && lines.Scan() {
		if line, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
			var e map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &e))
			byType[e["type"].(string)] = e
		}
	}

	require.Equal(t, "/protobuf.TestService/Greetings", byType["matched"]["target"])
	require.NotEmpty(t, byType["matched"]["mock_id"])
	require.Equal(t, "/protobuf.TestService/List", byType["unmatched"]["target"])
}

func TestAdmin_AGRPCServerWhoseAdminCantListen(t *testing.T) {
	// The admin API failing to listen fails the start, and nothing listens.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	m := MustNew(Setup().Admin(taken.Addr().String()))
	t.Cleanup(m.Close)

	require.ErrorContains(t, m.Start(), "admin: ")
	require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)
	require.Empty(t, m.Addr())
}

func statusCode(err error) codes.Code {
	return status.Code(err)
}

func TestAdmin_StreamsTheLogOfALoggerItBuilds(t *testing.T) {
	// A server that builds its logger streams its log lines; one given a logger, as adminServer's, doesn't.
	m := MustNew(Setup().Admin(""))
	m.MustStart()
	t.Cleanup(m.Close)

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.AdminURL()+"/logs", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))

	given, _ := adminServer(t)
	status, _ := call(t, http.MethodGet, given.AdminURL()+"/logs", "")
	require.Equal(t, http.StatusNotImplemented, status)
}
