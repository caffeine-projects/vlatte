package mockgrpc

import (
	"bufio"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
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

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
	"github.com/caffeine-projects/vlatte/webhook"
)

// adminServer starts a gRPC mock server with its admin API, and a client of it.
func adminServer(t *testing.T) (*Server, pb.TestServiceClient) {
	t.Helper()

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger))
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
	// gRPC mocks have no JSON Schema of their mock files: the operation answers 501, and says so.
	m, _ := adminServer(t)

	status, body := call(t, http.MethodGet, m.AdminURL()+"/schema", "")
	require.Equal(t, http.StatusNotImplemented, status)
	require.Contains(t, body, `"code": "not_supported"`)
}

func TestAdmin_RecordsAService(t *testing.T) {
	// A recording started through the admin API takes the calls of the mock server to its target, and once stopped,
	// the server answers them from the mock files it wrote, the target gone.
	target := serve(t)
	target.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "from the target"})))

	m, client := adminServer(t)
	dir := t.TempDir()

	settings := `{"target": "` + target.Addr() + `", "dir": "` + filepath.ToSlash(dir) + `", "format": "yaml"}`
	status, body := call(t, http.MethodPost, m.AdminURL()+"/recording", settings)
	require.Equal(t, http.StatusCreated, status, body)

	_, err := client.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	status, body = call(t, http.MethodDelete, m.AdminURL()+"/recording", "")
	require.Equal(t, http.StatusOK, status, body)

	var result struct {
		Recordings []struct {
			File string `json:"file"`
		} `json:"recordings"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &result))
	require.Len(t, result.Recordings, 1)
	require.True(t, strings.HasSuffix(result.Recordings[0].File, ".mock.yaml"), result.Recordings[0].File)

	target.Close()

	res, err := client.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "from the target", res.GetMessage(), "served from the recording")
}

func TestAdmin_RecordingSettingsWithProblems(t *testing.T) {
	// The settings of a recording are those of the grpc.record section of the server's settings, checked the same
	// way, and by the recording's own checks.
	m, _ := adminServer(t)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/recording", `{"format": "xml", "frmat": "json"}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Contains(t, body, `"key": "format"`)
	require.Contains(t, body, `"key": "frmat"`)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/recording", `{"methods": ["Greetings", " "]}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Contains(t, body, "recorder: method 1 is empty")

	_, body = call(t, http.MethodGet, m.AdminURL()+"/recording", "")
	require.JSONEq(t, `{"active": false, "calls": 0, "mocks": 0, "dropped": 0, "failed": 0}`, body, "nothing started")
}

func TestAdmin_AnEmptyRecordingBodyKeepsTheServersSettings(t *testing.T) {
	dir := t.TempDir()
	m := MustNew(Setup().Silent().Admin(admin.MustNew()).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/recording", "")
	require.Equal(t, http.StatusCreated, status, body)
	require.True(t, m.IsRecording())
	require.DirExists(t, filepath.Join(dir, "_mocks_recorded"))
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

func TestAdmin_AFuncMockCantBeDescribed(t *testing.T) {
	// A mock file can't hold a Func reply: reading the mock fails, and says why, as it does on a mockhttp server.
	m, _ := adminServer(t)
	m.MustMock(Method("Greetings").ID("func").Reply(Func(func(context.Context, *Call) error { return nil })))

	status, body := call(t, http.MethodGet, m.AdminURL()+"/mocks/func", "")
	require.Equal(t, http.StatusInternalServerError, status, body)
	require.Contains(t, body, `"code": "describe_failed"`)
	require.Contains(t, body, "a Func reply can't be described as a mock file")
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
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
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
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).Journal(""))
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

	m := MustNew(Setup().Admin(admin.MustNew(admin.Addr(taken.Addr().String()))))
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

	m := MustNew(Setup().Listener(l).Admin(admin.MustNew(admin.Addr(taken.Addr().String()))))
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
	m := MustNew(Setup().Admin(admin.MustNew()))
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
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).ConfigSources())
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
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
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
			m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
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
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, "QUERY", m.AdminURL()+"/requests", `{"request": {"fields": {"message": "bye"}}}`)
	require.Equal(t, http.StatusBadRequest, status, body)
	require.Contains(t, body, "request.method is required")
}

func TestAdmin_SavesUnderARelativeRootDir(t *testing.T) {
	// The saved files are under the root directory once: with a relative one, the server loads them from where it
	// wrote them, and the mocks still answer.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("fixtures", 0o755))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir("fixtures"))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{"id": "saved", "request": {"method": "Greetings"}, "response": {"message": {"message": "from file"}}}`
	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusOK, status, body)
	require.FileExists(t, filepath.Join("fixtures", "_mocks", "saved.mock.yaml"))

	got, _ := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
	require.Equal(t, "from file", got)
}

func TestAdmin_ASaveThatFailsPutsBackTheFileItReplaced(t *testing.T) {
	// A saved file of the same id is replaced by the save: when the save fails, it's back as it was.
	dir := t.TempDir()
	saved := filepath.Join(dir, "_mocks")
	require.NoError(t, os.Mkdir(saved, 0o755))

	before := []byte("request: {method: Greetings}\nresponse: {message: {message: before}}\n")
	require.NoError(t, os.WriteFile(filepath.Join(saved, "kept.mock.yaml"), before, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(saved, "broken.mock.yaml"), []byte("request: {method: Nope}"), 0o600))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{"id": "kept", "request": {"method": "Greetings"}, "response": {"message": {"message": "kept"}}}`
	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusInternalServerError, status, body)

	after, err := os.ReadFile(filepath.Join(saved, "kept.mock.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestAdmin_ASaveThatCantDescribeAMockWritesNothing(t *testing.T) {
	// Every mock is checked before any file is written: one whose body file is gone fails the save, and its file
	// isn't written half way.
	wd := t.TempDir()
	t.Chdir(wd)
	require.NoError(t, os.WriteFile("items.json", []byte(`[{"key": "a"}]`), 0o600))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(wd))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks",
		`{"id": "items", "request": {"method": "List"}, "response": {"body_file": "items.json"}}`)
	require.Equal(t, http.StatusCreated, status, body)
	require.NoError(t, os.Remove("items.json"))

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.NotEqual(t, http.StatusOK, status, body)
	require.Contains(t, body, "items")
	require.NoDirExists(t, filepath.Join(wd, "_mocks"))
}

func TestAdmin_ASaveThatCantWriteAFileKeepsTheMocksItAdded(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "_mocks", "kept.mock.yaml"), 0o755))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `{"id": "kept", "request": {"method": "Greetings"}, "response": {"message": {"message": "kept"}}}`
	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusInternalServerError, status, body)

	got, _ := greetWith(t, pb.NewTestServiceClient(m.MustDial()), "hi")
	require.Equal(t, "kept", got)
}

func TestAdmin_SavesABidiMockWithBodyFilesInItsRules(t *testing.T) {
	// The body files of a bidi reply's rules, otherwise and on_end open from the working directory too: saved, each
	// names its file from the saved file's directory.
	wd := t.TempDir()
	t.Chdir(wd)
	require.NoError(t, os.WriteFile("hello.json", []byte(`{"message": "hello"}`), 0o600))

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).ConfigSources())
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", `{"id": "chat", "request": {"method": "Chat"}, "bidi": {
		"on": [{"when": ["field", "message", ["eq", "hi"]], "response": {"body_file": "hello.json"}}],
		"on_end": {"body_file": "hello.json"}
	}}`)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{"dir": "saved"}`)
	require.Equal(t, http.StatusOK, status, body)

	saved, err := os.ReadFile(filepath.Join(wd, "saved", "chat.mock.yaml"))
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(saved), filepath.Join("..", "hello.json")), "%s", saved)
}

func TestAdmin_DescribesAndSavesTheEnabledStateItSet(t *testing.T) {
	// Turning a mock of a document off or on doesn't change its document: its description and its saved file say
	// what the admin API set, so the saved mock loads back as it was.
	dir := t.TempDir()
	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(dir))
	m.MustStart()
	t.Cleanup(m.Close)
	c := pb.NewTestServiceClient(m.MustDial())

	for _, doc := range []string{
		`{"id": "off", "request": {"method": "Greetings", "fields": {"message": "off"}}, "response": {"message": {"message": "off"}}}`,
		`{"id": "on", "enabled": false, "request": {"method": "Greetings", "fields": {"message": "on"}}, "response": {"message": {"message": "on"}}}`,
	} {
		status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
		require.Equal(t, http.StatusCreated, status, body)
	}

	for id, enabled := range map[string]string{"off": "false", "on": "true"} {
		status, body := call(t, http.MethodPatch, m.AdminURL()+"/mocks/"+id, `{"enabled": `+enabled+`}`)
		require.Equal(t, http.StatusOK, status, body)
	}

	_, body := call(t, http.MethodGet, m.AdminURL()+"/mocks/off", "")
	require.JSONEq(t, `{"id": "off", "enabled": false, "request": {"method": "Greetings", "fields": {"message": "off"}}, "response": {"message": {"message": "off"}}}`, body)

	_, body = call(t, http.MethodGet, m.AdminURL()+"/mocks/on", "")
	require.JSONEq(t, `{"id": "on", "request": {"method": "Greetings", "fields": {"message": "on"}}, "response": {"message": {"message": "on"}}}`, body)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusOK, status, body)

	_, code := greetWith(t, c, "off")
	require.Equal(t, codes.NotFound, code, "saved off, it loads back off")

	got, _ := greetWith(t, c, "on")
	require.Equal(t, "on", got, "saved on, it loads back on")
}

func TestRelocate_MovesTheBodyFilesOfWebhooks(t *testing.T) {
	// The mock file format opens the body files of webhooks, and of their multipart parts, from the mock file's
	// directory: those move. Other listeners read their parameters as they will, so theirs stay.
	const doc = `{
		"response": {"body_file": %[1]q},
		"on_complete": [
			{"name": "webhook", "parameters": {
				"body_file": %[1]q,
				"multipart": {"parts": [{"body_file": %[1]q}]},
				"multipart_template": {"parts": [{"body_file": %[1]q}]}
			}},
			{"name": "audit", "parameters": {"body_file": "kept"}}
		]
	}`

	up := filepath.Join("..", "..")

	var got, want map[string]any
	require.NoError(t, json.Unmarshal(fmt.Appendf(nil, doc, "hook.txt"), &got))
	require.NoError(t, json.Unmarshal(fmt.Appendf(nil, doc, filepath.Join(up, "hook.txt")), &want))

	relocate(got, up)
	require.Equal(t, want, got)
}

func TestAdmin_SavesAMockWithAWebhookBodyFile(t *testing.T) {
	// A webhook's body_file opens from the mock file's directory: saved elsewhere, it names the same file.
	wd := t.TempDir()
	t.Chdir(wd)
	require.NoError(t, os.WriteFile("hook.txt", []byte("hook"), 0o600))

	wh, err := webhook.New()
	require.NoError(t, err)

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).ConfigSources().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", `{"id": "hooked", "request": {"method": "Greetings"},
		"response": {"message": {"message": "hi"}},
		"on_complete": [{"name": "webhook", "parameters": {"url": "http://hooks.example/hook", "body_file": "hook.txt"}}]}`)
	require.Equal(t, http.StatusCreated, status, body)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{"dir": "saved/mocks"}`)
	require.Equal(t, http.StatusOK, status, body)

	saved, err := os.ReadFile(filepath.Join(wd, "saved", "mocks", "hooked.mock.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(saved), "body_file: "+filepath.Join("..", "..", "hook.txt"))
}

// spyEngine is a template engine that calls during before it compiles each template.
type spyEngine struct {
	vlatte.TemplateEngine

	during func()
}

func (e spyEngine) Compile(name, text string) (vlatte.Template, error) {
	e.during()
	return e.TemplateEngine.Compile(name, text)
}

func TestAdmin_ASaveKeepsServingTheMocksItAddedWhileTheFilesLoad(t *testing.T) {
	// A saved file's mock takes the place of the admin API's in one step: a call while the files build, as their
	// templates compile, still finds the admin API's mock.
	engine, err := vlatte.NewGoTemplateEngine()
	require.NoError(t, err)

	var (
		c    pb.TestServiceClient
		seen []codes.Code
	)

	spy := spyEngine{TemplateEngine: engine, during: func() {
		if c != nil {
			_, code := greetWith(t, c, "hi")
			seen = append(seen, code)
		}
	}}

	logger := zerolog.Nop()
	m := MustNew(Setup().Admin(admin.MustNew()).Logger(&logger).RootDir(t.TempDir()).TemplateEngine(spy))
	m.MustStart()
	t.Cleanup(m.Close)

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", `{"id": "saved", "request": {"method": "Greetings"},
		"response": {"message": "{\"message\": \"{{ .Request.Message.message }}\"}", "template": true}}`)
	require.Equal(t, http.StatusCreated, status, body)

	c = pb.NewTestServiceClient(m.MustDial())

	status, body = call(t, http.MethodPost, m.AdminURL()+"/mocks/save", `{}`)
	require.Equal(t, http.StatusOK, status, body)

	require.NotEmpty(t, seen)
	for _, code := range seen {
		require.Equal(t, codes.OK, code)
	}
}
