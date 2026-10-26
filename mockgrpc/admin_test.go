package mockgrpc

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte/match"
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
	scope := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "bye"})))
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
	// gRPC mocks don't record what they proxy: that operation answers 501, and says so.
	m, _ := adminServer(t)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/recording", `{}`)
	require.Equal(t, http.StatusNotImplemented, status)
	require.Contains(t, body, `"code": "not_supported"`)
}

func TestAdmin_AddsDescribesAndResetsMocksOfDocuments(t *testing.T) {
	// The admin API answered 501 to adding a gRPC mock, or reading one: gRPC mocks had no document. They have the
	// mock file format now, so the admin API adds them, returns them as written, and a reset removes them.
	m, client := adminServer(t)
	doc := `{"id": "greet", "request": {"method": "Greetings", "fields": {"message": "hi"}}, "response": {"message": {"message": "from admin"}}}`

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)

	res, err := client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)
	require.Equal(t, "from admin", res.GetMessage())

	status, body = call(t, http.MethodGet, m.AdminURL()+"/mocks/greet", "")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, doc, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks", `{"request": {"method": "Nope"}, "response": {}}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, body, "unknown method")

	status, _ = call(t, http.MethodPost, m.AdminURL()+"/reset", "")
	require.Equal(t, http.StatusNoContent, status)

	_, err = client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, statusCode(err), "a reset removes the mocks the admin API added")
}

func TestAdmin_DescribesAMockBuiltInGo(t *testing.T) {
	m, _ := adminServer(t)
	m.MustMock(Method("Greetings").ID("go").Priority(2).Header("x-team", match.Equal("core")).
		Reply(Message(&pb.HiRes{Message: "bye"}).Trailer("t", "v")))

	status, body := call(t, http.MethodGet, m.AdminURL()+"/mocks/go", "")
	require.Equal(t, http.StatusOK, status, body)
	require.JSONEq(t, `{
		"id": "go",
		"priority": 2,
		"request": {"method": "Greetings", "header": {"x-team": ["eq", "core"]}},
		"response": {"messages": [{"message": "bye"}], "trailer": {"t": ["v"]}}
	}`, body)
}

func TestAdmin_MovesScenarios(t *testing.T) {
	m, client := adminServer(t)
	m.MustMock(
		Method("Greetings").ScenarioIs("s").ScenarioStateIs("started").Reply(Message(&pb.HiRes{Message: "first"})),
		Method("Greetings").ScenarioIs("s").ScenarioStateIs("later").Reply(Message(&pb.HiRes{Message: "later"})),
	)

	status, body := call(t, http.MethodPut, m.AdminURL()+"/scenarios/s", `{"state": "later"}`)
	require.Equal(t, http.StatusOK, status, body)

	res, err := client.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "later", res.GetMessage())
}

func TestAdmin_SavesTheMocksItAddedAsFiles(t *testing.T) {
	// The mocks the admin API adds go with the server: saved, they load from files, as written, on later starts.
	dir := t.TempDir()
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{"id": "saved", "request": {"method": "Greetings"}, "response": {"message": {"message": "from file"}}}`
	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	require.FileExists(t, filepath.Join(dir, "_mocks", "saved.mock.yaml"))

	saved, err := m.storage.Pick(t.Context(), "saved")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "_mocks", "saved.mock.yaml"), saved.Origin(), "the file serves it now")

	got, _ := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
	require.Equal(t, "from file", got)
}

func TestAdmin_QueriesTheJournalWithARequestOfAMockFile(t *testing.T) {
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger).Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	c := pb.NewTestServiceClient(m.MustDial())
	for _, msg := range []string{"hi", "bye"} {
		_, err := c.Greetings(t.Context(), &pb.HiReq{Message: msg})
		require.NoError(t, err)
	}

	status, body := call(t, "QUERY", m.AdminURL()+"/requests", `{"request": {"method": "Greetings", "fields": {"message": "bye"}}}`)
	require.Equal(t, http.StatusOK, status, body)

	var page struct {
		Requests []map[string]any `json:"requests"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &page))
	require.Len(t, page.Requests, 1, body)
}

func TestAdmin_StreamsTheCalls(t *testing.T) {
	// A client follows the calls the mocks answer, and those none matched, as HTTP mock servers' clients do.
	m, client := adminServer(t)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "bye"})))

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

	require.NoError(t, lines.Err())
	require.Equal(t, "/protobuf.TestService/Greetings", byType["matched"]["target"])
	require.NotEmpty(t, byType["matched"]["mock_id"])
	require.Equal(t, "/protobuf.TestService/List", byType["unmatched"]["target"])
}

// failingReply makes repliers that fail every call with an error that has no gRPC status.
type failingReply struct{}

func (failingReply) NewReplier(*Server, []protoreflect.MethodDescriptor) (Replier, error) {
	return failingReply{}, nil
}

func (failingReply) Reply(context.Context, *Call) error {
	return errors.New("reply: broken")
}

func TestAdmin_StreamsTheCallsAMockFailed(t *testing.T) {
	// A reply that fails without a gRPC status failed the call, which isn't the mock's answer: the client follows an
	// error, with what failed.
	m, client := adminServer(t)
	m.MustMock(Method("Greetings").Reply(failingReply{}))

	// A missing event fails the test, rather than hangs it.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, m.AdminURL()+"/events", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	_, err = client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.Internal, statusCode(err))

	lines := bufio.NewScanner(res.Body)
	for lines.Scan() {
		if line, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
			var e map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &e))
			require.Equal(t, "error", e["type"])
			require.Equal(t, "/protobuf.TestService/Greetings", e["target"])
			require.Contains(t, e["message"], "reply: broken")
			require.NoError(t, lines.Err())

			return
		}
	}

	t.Fatal("no event of the call")
}

func TestAdmin_NoURLWithoutTheAdminAPI(t *testing.T) {
	m := MustNew()
	t.Cleanup(m.Close)

	require.Empty(t, m.AdminURL())
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

func TestAdmin_AGRPCStartThatFailsClosesTheListenerItWasGiven(t *testing.T) {
	// Start is all or nothing: when the admin API can't listen, nothing does, the listener the server was given
	// included, so clients are refused at once rather than left waiting on a server that isn't there.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Listener(l).Admin(taken.Addr().String()))
	t.Cleanup(m.Close)

	require.ErrorContains(t, m.Start(), "admin: ")

	// An open listener would wait for a client: the deadline fails the test instead.
	if tcp, ok := l.(*net.TCPListener); ok {
		_ = tcp.SetDeadline(time.Now().Add(time.Second))
	}

	_, err = l.Accept()
	require.ErrorIs(t, err, net.ErrClosed)

	_, err = net.DialTimeout("tcp", l.Addr().String(), time.Second)
	require.Error(t, err, "nothing listens")

	require.Empty(t, m.Addr())
	require.ErrorIs(t, m.Start(), grpc.ErrServerStopped)
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

func TestAdmin_SavesAMockWithABodyFile(t *testing.T) {
	// A relative body_file of a mock the admin API added opens from the working directory: saved into a directory of
	// its own, the file names it from there, and serves the same messages.
	wd := t.TempDir()
	t.Chdir(wd)
	require.NoError(t, os.WriteFile("items.json", []byte(`[{"key": "a"}, {"key": "b"}]`), 0o600))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger).ConfigSources())
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks",
		`{"id": "items", "request": {"method": "List"}, "response": {"body_file": "items.json"}}`)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{"dir": "saved/mocks"}`)
	require.Equal(t, http.StatusOK, status, body)

	saved, err := os.ReadFile(filepath.Join(wd, "saved", "mocks", "items.mock.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(saved), filepath.Join("..", "..", "items.json"))

	require.Equal(t, []string{"a", "b"}, listKeys(t, pb.NewTestServiceClient(m.MustDial())))
}

func TestAdmin_SavesNothingWithoutMocksItAdded(t *testing.T) {
	// Mocks of Go code or of files are not the admin API's to save: with none of its own, it writes no file.
	dir := t.TempDir()
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	require.NoDirExists(t, filepath.Join(dir, "_mocks"))
}

func TestAdmin_ASaveThatFailsKeepsTheMocksItAdded(t *testing.T) {
	// A save that can't write its files, or whose files don't load, leaves the server as it was: the admin API's
	// mocks still answer, and no file of theirs is left behind.
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, dir string)
	}{
		{"dir is a file", func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "_mocks"), nil, 0o600))
		}},
		{"dir has a broken mock file", func(t *testing.T, dir string) {
			require.NoError(t, os.Mkdir(filepath.Join(dir, "_mocks"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "_mocks", "broken.mock.yaml"), []byte("request: {method: Nope}"), 0o600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.prepare(t, dir)

			logger := zerolog.Nop()
			m := MustNew(Setup().Admin("").Logger(&logger).RootDir(dir))
			m.MustStart()
			t.Cleanup(m.Close)

			doc := `{"id": "kept", "request": {"method": "Greetings"}, "response": {"message": {"message": "kept"}}}`
			status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
			require.Equal(t, http.StatusCreated, status, body)

			status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
			require.Equal(t, http.StatusInternalServerError, status, body)
			require.NoFileExists(t, filepath.Join(dir, "_mocks", "kept.mock.yaml"))

			got, _ := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
			require.Equal(t, "kept", got)
		})
	}
}

func TestAdmin_AQueryWithoutAMethodSaysSo(t *testing.T) {
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin("").Logger(&logger).Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, "QUERY", m.AdminURL()+"/requests", `{"request": {"fields": {"message": "bye"}}}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Contains(t, body, "request.method is required")
}
