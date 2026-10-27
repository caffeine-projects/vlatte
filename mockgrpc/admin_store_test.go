package mockgrpc

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// storeServer starts a gRPC mock server of cb with its admin API, and a client of it.
func storeServer(t *testing.T, cb *ConfigBuilder) (*Server, pb.TestServiceClient) {
	t.Helper()

	logger := zerolog.Nop()
	m := MustNew(cb.Admin(admin.MustNew()).Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	return m, pb.NewTestServiceClient(m.MustDial())
}

// addMock adds the mock of doc, a JSON document, through the admin API of m.
func addMock(t *testing.T, m *Server, doc string) {
	t.Helper()

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks", doc)
	require.Equal(t, http.StatusCreated, status, body)
}

// greeting returns the message the Greetings method of c answers, or the code of its failure.
func greeting(t *testing.T, c pb.TestServiceClient) (string, codes.Code) {
	t.Helper()

	res, err := c.Greetings(t.Context(), &pb.HiReq{})

	return res.GetMessage(), statusCode(err)
}

func TestFileStore_KeepsTheAdminMocksAcrossRestarts(t *testing.T) {
	// The mocks the admin API adds, and the changes it makes to them, reach the files of the store at once: the next
	// server serves them as they were, a relative body file included.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("bodies", 0o700))
	require.NoError(t, os.WriteFile("bodies/hello.json", []byte(`{"message": "hello from a file"}`), 0o600))

	m, _ := storeServer(t, Setup().AdminStore(FileStore("admin")))
	addMock(t, m, `{"id": "hello", "request": {"method": "Greetings"}, "response": {"body_file": "bodies/hello.json"}}`)
	addMock(t, m, `{"id": "off", "request": {"method": "List"}, "response": {"messages": []}}`)

	status, body := call(t, http.MethodPatch, m.AdminURL()+"/mocks/off", `{"enabled": false}`)
	require.Equal(t, http.StatusOK, status, body)

	require.FileExists(t, "admin/hello.mock.yaml")
	require.FileExists(t, "admin/off.mock.yaml")
	m.Close()

	next, c := storeServer(t, Setup().AdminStore(FileStore("admin")))

	msg, code := greeting(t, c)
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hello from a file", msg)

	stream, err := c.List(t.Context(), &pb.ListReq{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.NotFound, statusCode(err), "off stays disabled")

	_, body = call(t, http.MethodGet, next.AdminURL()+"/mocks/hello", "")
	require.Contains(t, body, `"body_file": "bodies/hello.json"`)

	_, body = call(t, http.MethodGet, next.AdminURL()+"/mocks", "")
	require.Equal(t, 2, strings.Count(body, `"origin": "admin"`), "the admin API's mocks still")
}

func TestFileStore_ServesWhatItsFilesHoldOnReloadAndReset(t *testing.T) {
	// A person edits, adds or removes a file of the store while the server runs: a reload serves what the files hold.
	// A reset between tests brings the server back to its sources, the store included.
	dir := t.TempDir()
	m, c := storeServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "hello", "request": {"method": "Greetings"}, "response": {"message": {"message": "first"}}}`)

	path := filepath.Join(dir, "hello.mock.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "first", "edited", 1)), 0o600))

	status, body := call(t, http.MethodPost, m.AdminURL()+"/mocks/reload", "")
	require.Equal(t, http.StatusNoContent, status, body)

	msg, _ := greeting(t, c)
	require.Equal(t, "edited", msg)

	status, body = call(t, http.MethodPost, m.AdminURL()+"/reset", "")
	require.Equal(t, http.StatusNoContent, status, body)

	msg, _ = greeting(t, c)
	require.Equal(t, "edited", msg, "the reset keeps what the store holds")

	require.NoError(t, os.Remove(path))
	require.NoError(t, m.Reload())

	_, code := greeting(t, c)
	require.Equal(t, codes.NotFound, code, "its file is gone")
}

func TestFileStore_RemovingMocksRemovesTheirFiles(t *testing.T) {
	dir := t.TempDir()
	m, _ := storeServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "a", "request": {"method": "Greetings"}, "response": {"message": {}}}`)
	addMock(t, m, `{"id": "b", "request": {"method": "Greetings"}, "response": {"message": {}}}`)

	status, _ := call(t, http.MethodDelete, m.AdminURL()+"/mocks/a", "")
	require.Equal(t, http.StatusNoContent, status)
	require.NoFileExists(t, filepath.Join(dir, "a.mock.yaml"))
	require.FileExists(t, filepath.Join(dir, "b.mock.yaml"))

	status, _ = call(t, http.MethodDelete, m.AdminURL()+"/mocks", "")
	require.Equal(t, http.StatusNoContent, status)
	require.NoFileExists(t, filepath.Join(dir, "b.mock.yaml"))
}

func TestFileStore_AMockFileWins(t *testing.T) {
	// A mock file's mock wins over a mock of the store with its id, as over any mock the admin API adds. A relative
	// store directory starts at the root directory, as mock file patterns do.
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "mocks"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(root, "admin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mocks", "x.yaml"),
		[]byte("id: x\nrequest: {method: Greetings}\nresponse: {message: {message: from the mock file}}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "admin", "x.mock.yaml"),
		[]byte("id: x\nrequest: {method: Greetings}\nresponse: {message: {message: from the store}}\n"), 0o600))

	_, c := storeServer(t, Setup().RootDir(root).MockFiles("mocks/*.yaml").AdminStore(FileStore("admin")))

	msg, _ := greeting(t, c)
	require.Equal(t, "from the mock file", msg)
}

func TestFileStore_RefusesAMockThatCantBeDescribed(t *testing.T) {
	// A mock file can't hold a Func reply: the store refuses it, and keeps no file of it.
	dir := t.TempDir()
	m := MustNew(Setup().Silent())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	mock, err := Method("Greetings").ID("func").Reply(Func(func(context.Context, *Call) error { return nil })).Build(m)
	require.NoError(t, err)

	err = store.Store(t.Context(), mock)
	require.ErrorContains(t, err, `server: describing mock "func": `)
	require.NoFileExists(t, filepath.Join(dir, "func.mock.yaml"))
}

func TestAdminStore_NeedsAnAdminAPI(t *testing.T) {
	// A persistent store of the admin API's mocks without an admin API would keep nothing: it is a mistake of the
	// setup, not a store to ignore.
	_, err := New(Setup().Silent().AdminStore(FileStore(t.TempDir())))

	require.ErrorIs(t, err, errAdminStoreWithoutAdmin)
	require.EqualError(t, err, "server: AdminStore needs an admin API: pass ConfigBuilder.Admin")
}

func TestAdminStore_AStoreThatCantBeMadeFailsTheServer(t *testing.T) {
	errDown := errors.New("database down")

	_, err := New(Setup().Silent().Admin(admin.MustNew()).AdminStore(func(*Server) (vlatte.Store[*Mock], error) {
		return nil, errDown
	}))

	require.ErrorIs(t, err, errDown)
	require.ErrorContains(t, err, "server: admin store: ")
}

func TestEncodeMock_DecodesBack(t *testing.T) {
	// A mock a store keeps as its description, such as a document of a database, is built back as the same mock, one
	// of the admin API's.
	m := MustNew(Setup().Silent())
	built, err := Method("Greetings").ID("greet").Reply(Message(&pb.HiRes{Message: "hi"})).Build(m)
	require.NoError(t, err)

	data, err := EncodeMock(built, "yaml")
	require.NoError(t, err)

	back, err := DecodeMock(m, data, "yaml")
	require.NoError(t, err)

	again, err := EncodeMock(back, "yaml")
	require.NoError(t, err)
	require.Equal(t, string(data), string(again))
	require.Equal(t, "admin", back.Origin())

	_, err = EncodeMock(back, "toml")
	require.ErrorContains(t, err, `format "toml"`)
}
