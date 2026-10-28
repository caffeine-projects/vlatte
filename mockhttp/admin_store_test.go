package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

var _ vlatte.Store[*Mock] = (*docsStore)(nil)

// docsStore is a persistent store of a user, such as a database collection: it keeps the descriptions of mocks, as
// EncodeMock writes them, by ID, and builds mocks back with DecodeMock. Servers that share docs share the store.
type docsStore struct {
	app  *Server
	mu   *sync.Mutex
	docs map[string][]byte
}

// docsAdminStore returns an AdminStore of docs, which every server it makes a store for shares.
func docsAdminStore(docs map[string][]byte) AdminStore {
	mu := &sync.Mutex{}

	return func(app *Server) (vlatte.Store[*Mock], error) {
		return &docsStore{app: app, mu: mu, docs: docs}, nil
	}
}

func (s *docsStore) List(ctx context.Context) (iter.Seq[*Mock], error) {
	all, err := s.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	return slices.Values(slices.DeleteFunc(all, func(m *Mock) bool { return !m.Enabled() })), nil
}

func (s *docsStore) Store(ctx context.Context, mocks ...*Mock) error {
	return s.Replace(ctx, nil, mocks...)
}

func (s *docsStore) Replace(_ context.Context, old []*Mock, mocks ...*Mock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	docs := maps.Clone(s.docs)
	for _, m := range old {
		delete(docs, m.ID())
	}

	for _, m := range mocks {
		if _, taken := docs[m.ID()]; taken {
			return fmt.Errorf("%w: %q", vlatte.ErrDuplicateID, m.ID())
		}

		data, err := EncodeMock(m, "json")
		if err != nil {
			return err
		}

		docs[m.ID()] = data
	}

	clear(s.docs)
	maps.Copy(s.docs, docs)

	return nil
}

func (s *docsStore) Pick(_ context.Context, id string) (*Mock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, ok := s.docs[id]
	if !ok {
		return nil, vlatte.ErrNotFound
	}

	return DecodeMock(s.app, data, "json")
}

func (s *docsStore) PickAll(context.Context) ([]*Mock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all := make([]*Mock, 0, len(s.docs))
	for _, id := range slices.Sorted(maps.Keys(s.docs)) {
		m, err := DecodeMock(s.app, s.docs[id], "json")
		if err != nil {
			return nil, err
		}

		all = append(all, m)
	}

	return all, nil
}

func (s *docsStore) Delete(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		delete(s.docs, id)
	}

	return nil
}

func (s *docsStore) Health(context.Context) error {
	return nil
}

func (s *docsStore) Clear(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.docs)

	return nil
}

// addMock adds the mock of doc, a JSON document, through the admin API of m.
func addMock(t *testing.T, m *Server, doc string) {
	t.Helper()

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks", nil, []byte(doc))
	require.Equal(t, http.StatusCreated, res.StatusCode, string(body))
}

func TestFileStore_KeepsTheAdminMocksAcrossRestarts(t *testing.T) {
	// The mocks the admin API adds, and the changes it makes to them, reach the files of the store at once: the next
	// server serves them as they were, a relative body file included.
	t.Chdir(t.TempDir())
	require.NoError(t, os.Mkdir("bodies", 0o700))
	require.NoError(t, os.WriteFile("bodies/hello.txt", []byte("hello from a file"), 0o600))

	m := adminServer(t, Setup().AdminStore(FileStore("admin")))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body_file": "bodies/hello.txt"}}`)
	addMock(t, m, `{"id": "off", "request": {"method": "GET", "path": "/off"}, "response": {"status": 200}}`)

	res, body := do(t, http.MethodPatch, m.AdminURL()+"/mocks/off", nil, []byte(`{"enabled": false}`))
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	require.FileExists(t, "admin/hello.mock.yaml")
	require.FileExists(t, "admin/off.mock.yaml")
	m.Close()

	next := adminServer(t, Setup().AdminStore(FileStore("admin")))

	res, body = do(t, http.MethodGet, next.URL("/hello"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello from a file", string(body))

	res, _ = do(t, http.MethodGet, next.URL("/off"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode, "off stays disabled")

	_, body = do(t, http.MethodGet, next.AdminURL()+"/mocks/hello", nil, nil)
	require.Contains(t, string(body), `"body_file": "bodies/hello.txt"`)

	_, body = do(t, http.MethodGet, next.AdminURL()+"/mocks", nil, nil)
	require.Equal(t, 2, strings.Count(string(body), `"origin": "admin"`), "the admin API's mocks still")
}

func TestFileStore_ServesWhatItsFilesHoldOnReload(t *testing.T) {
	// A person edits, adds or removes a file of the store while the server runs: a reload serves what the files hold.
	dir := t.TempDir()
	m := adminServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body": "first"}}`)

	path := filepath.Join(dir, "hello.mock.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "first", "edited", 1)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "added.mock.json"),
		[]byte(`{"id": "added", "request": {"method": "GET", "path": "/added"}, "response": {"status": 201}}`), 0o600))

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/reload", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	_, body = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, "edited", string(body))

	res, _ = do(t, http.MethodGet, m.URL("/added"), nil, nil)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	require.NoError(t, os.Remove(path))
	require.NoError(t, m.Reload())

	res, _ = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode, "its file is gone")
}

func TestFileStore_ResetKeepsWhatItHolds(t *testing.T) {
	// A reset between tests brings the server back to its sources: the mocks of the store come back from it, and
	// stay in it.
	dir := t.TempDir()
	m := adminServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200}}`)

	res, body := do(t, http.MethodPost, m.AdminURL()+"/reset", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	res, _ = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.FileExists(t, filepath.Join(dir, "hello.mock.yaml"))
}

func TestFileStore_AReloadTheStoreFailsKeepsTheMockFilesAsTheyWere(t *testing.T) {
	// A reload is all or nothing: the store fails, so the mock file edited meanwhile isn't served either.
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "mocks"), 0o700))

	file := filepath.Join(root, "mocks", "x.yaml")
	require.NoError(t, os.WriteFile(file, []byte("request: {method: GET, path: /x}\nresponse: {status: 200, body: first}\n"), 0o600))

	m := adminServer(t, Setup().RootDir(root).MockFilePatterns("mocks/*.yaml").AdminStore(FileStore("admin")))

	require.NoError(t, os.WriteFile(file, []byte("request: {method: GET, path: /x}\nresponse: {status: 200, body: edited}\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "admin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "admin", "bad.mock.yaml"), []byte("id: bad\nrequest: {methd: GET}\n"), 0o600))

	require.ErrorContains(t, m.Reload(), "bad.mock.yaml")

	_, body := do(t, http.MethodGet, m.URL("/x"), nil, nil)
	require.Equal(t, "first", string(body))
}

func TestFileStore_RemovingMocksRemovesTheirFiles(t *testing.T) {
	dir := t.TempDir()
	m := adminServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "a", "request": {"method": "GET", "path": "/a"}, "response": {"status": 200}}`)
	addMock(t, m, `{"id": "b", "request": {"method": "GET", "path": "/b"}, "response": {"status": 200}}`)

	res, _ := do(t, http.MethodDelete, m.AdminURL()+"/mocks/a", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.NoFileExists(t, filepath.Join(dir, "a.mock.yaml"))
	require.FileExists(t, filepath.Join(dir, "b.mock.yaml"))

	res, _ = do(t, http.MethodDelete, m.AdminURL()+"/mocks", nil, nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.NoFileExists(t, filepath.Join(dir, "b.mock.yaml"))
}

func TestFileStore_RefusesAMockThatDoesntLoadBack(t *testing.T) {
	// A mock whose description doesn't load back, such as one of a matcher no registry has, would write a file that
	// fails every later start: it isn't stored.
	dir := t.TempDir()
	m := MustNew(Setup().Silent())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	mock, err := Postf("/x").ID("unregistered").Body(unregisteredMatcher{}).Reply(OK()).Build(m)
	require.NoError(t, err)

	err = store.Store(t.Context(), mock)
	require.ErrorContains(t, err, `server: mock "unregistered" can't be stored: its description doesn't load back: `)
	require.ErrorContains(t, err, `unknown matcher "unregistered"`)

	require.NoFileExists(t, filepath.Join(dir, "unregistered.mock.yaml"))
}

func TestFileStore_RefusesAMockThatCantBeDescribed(t *testing.T) {
	// #42: a mock with a Handler described as the default reply, so the store kept a mock that served another
	// answer. A mock file can't hold a Handler: it isn't stored.
	dir := t.TempDir()
	m := MustNew(Setup().Silent())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	mock, err := Getf("/h").ID("handler").Reply(Handler(func(http.ResponseWriter, *http.Request) {})).Build(m)
	require.NoError(t, err)

	err = store.Store(t.Context(), mock)
	require.ErrorContains(t, err, `server: describing mock "handler": `)
	require.ErrorIs(t, err, errFuncNotDescribed)

	require.NoFileExists(t, filepath.Join(dir, "handler.mock.yaml"))
}

func TestFileStore_AMockFileWins(t *testing.T) {
	// A mock file's mock wins over a mock of the store with its id, as over any mock the admin API adds.
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "mocks"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(root, "admin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mocks", "x.yaml"),
		[]byte("id: x\nrequest: {method: GET, path: /x}\nresponse: {status: 200, body: from the mock file}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "admin", "x.mock.yaml"),
		[]byte("id: x\nrequest: {method: GET, path: /x}\nresponse: {status: 200, body: from the store}\n"), 0o600))

	m := adminServer(t, Setup().RootDir(root).MockFilePatterns("mocks/*.yaml").AdminStore(FileStore("admin")))

	_, body := do(t, http.MethodGet, m.URL("/x"), nil, nil)
	require.Equal(t, "from the mock file", string(body))
}

func TestAdminStore_AStoreOfYours(t *testing.T) {
	// A store of a user, such as a database, keeps the mocks the admin API adds as EncodeMock describes them: the next
	// server builds them back with DecodeMock, and a change made to the store directly serves on a reload.
	docs := make(map[string][]byte)

	m := adminServer(t, Setup().AdminStore(docsAdminStore(docs)))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body": "stored"}}`)
	require.Contains(t, docs, "hello")
	m.Close()

	next := adminServer(t, Setup().AdminStore(docsAdminStore(docs)))

	_, body := do(t, http.MethodGet, next.URL("/hello"), nil, nil)
	require.Equal(t, "stored", string(body))

	docs["hello"] = []byte(strings.Replace(string(docs["hello"]), "stored", "changed in the store", 1))
	require.NoError(t, next.Reload())

	_, body = do(t, http.MethodGet, next.URL("/hello"), nil, nil)
	require.Equal(t, "changed in the store", string(body))
}

func TestAdminStore_SaveExportsAsMockFiles(t *testing.T) {
	// Save writes the admin API's mocks as mock files to commit, which serve from then on. The store keeps its own
	// copy, which gives way to the mock file.
	root := t.TempDir()
	m := adminServer(t, Setup().RootDir(root).AdminStore(FileStore("admin")))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200}}`)

	res, body := do(t, http.MethodPost, m.AdminURL()+"/mocks/save", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	require.FileExists(t, filepath.Join(root, "testdata", "vlatte", "_mocks", "hello.mock.yaml"))
	require.FileExists(t, filepath.Join(root, "admin", "hello.mock.yaml"))

	require.NoError(t, m.Reload())

	_, body = do(t, http.MethodGet, m.AdminURL()+"/mocks", nil, nil)
	require.Contains(t, string(body), `testdata/vlatte/_mocks/hello.mock.yaml"`, "served from the mock file")

	res, _ = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestAdminStore_NeedsAnAdminAPI(t *testing.T) {
	// A persistent store of the admin API's mocks without an admin API would keep nothing: it is a mistake of the
	// setup, not a store to ignore.
	_, err := New(Setup().Silent().AdminStore(FileStore(t.TempDir())))

	require.ErrorIs(t, err, errAdminStoreWithoutAdmin)
	require.EqualError(t, err, "server: AdminStore needs an admin API: pass ConfigBuilder.Admin")
}

func TestEncodeMock_DecodesBack(t *testing.T) {
	// A mock a store keeps as its description is built back as the same mock, one of the admin API's.
	m := adminServer(t, Setup())
	scope := m.MustMock(Getf("/hello").ID("hello").Reply(OK().PlainText("hi")))

	data, err := EncodeMock(scope.Mock("hello"), "yaml")
	require.NoError(t, err)

	back, err := DecodeMock(m, data, "yaml")
	require.NoError(t, err)
	t.Cleanup(func() { _ = back.Close() })

	again, err := EncodeMock(back, "yaml")
	require.NoError(t, err)
	require.Equal(t, string(data), string(again))
	require.Equal(t, "admin", back.Origin())

	_, err = EncodeMock(back, "toml")
	require.ErrorContains(t, err, `format "toml"`)
}

func TestFileStore_IsAStoreOfMockFiles(t *testing.T) {
	// The file store keeps a mock file per mock, by ID, as a store must: it refuses an ID it holds, builds new mocks
	// from its files, and removes their files.
	ctx := t.Context()
	dir := t.TempDir()
	m := adminServer(t, Setup())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	mock := func(id string) *Mock {
		built, err := DecodeMock(m, []byte(`{"id": "`+id+`", "request": {"method": "GET", "path": "/`+id+`"}, "response": {"status": 200}}`), "json")
		require.NoError(t, err)
		t.Cleanup(func() { _ = built.Close() })

		return built
	}

	a, b := mock("a"), mock("b")
	require.NoError(t, store.Store(ctx, a, b))
	require.ErrorIs(t, store.Store(ctx, mock("a")), vlatte.ErrDuplicateID)
	require.ErrorIs(t, store.Store(ctx, mock("c"), mock("c")), vlatte.ErrDuplicateID)
	require.NoFileExists(t, filepath.Join(dir, "c.mock.yaml"), "a refused call writes nothing")

	got, err := store.Pick(ctx, "a")
	require.NoError(t, err)
	require.NotSame(t, a, got, "built from its file")
	require.NoError(t, got.Close())

	_, err = store.Pick(ctx, "missing")
	require.ErrorIs(t, err, vlatte.ErrNotFound)

	b.Disable()
	require.NoError(t, store.Replace(ctx, []*Mock{b}, b))

	enabled, err := store.List(ctx)
	require.NoError(t, err)

	var listed []string
	for mock := range enabled {
		listed = append(listed, mock.ID())
		require.NoError(t, mock.Close())
	}

	require.Equal(t, []string{"a"}, listed, "b is disabled")

	// A file of a mock in another format, such as one a person wrote, gives way to the one the store writes.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.mock.json"),
		[]byte(`{"id": "j", "request": {"method": "GET", "path": "/j"}, "response": {"status": 200}}`), 0o600))
	require.NoError(t, store.Replace(ctx, []*Mock{mock("j")}, mock("j")))
	require.FileExists(t, filepath.Join(dir, "j.mock.yaml"))
	require.NoFileExists(t, filepath.Join(dir, "j.mock.json"))

	require.NoError(t, store.Delete(ctx, "a", "missing"))
	require.NoFileExists(t, filepath.Join(dir, "a.mock.yaml"))

	require.NoError(t, store.Clear(ctx))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestFileStore_FilesWithProblems(t *testing.T) {
	// A file with problems builds no mock at all, and says which file: the server serves what it served.
	ctx := t.Context()
	dir := t.TempDir()
	m := adminServer(t, Setup())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.mock.yaml"),
		[]byte("id: good\nrequest: {method: GET, path: /good}\nresponse: {status: 200}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.mock.yaml"), []byte("id: bad\nrequest: {methd: GET}\n"), 0o600))

	all, err := store.PickAll(ctx)
	require.ErrorContains(t, err, filepath.Join(dir, "bad.mock.yaml"))
	require.Nil(t, all)
}

func TestFileStore_ADirectoryItCantUse(t *testing.T) {
	// A directory that is a file, or a mock file that is a directory, fails each operation that needs it.
	ctx := t.Context()
	m := adminServer(t, Setup())

	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	store, err := FileStore(notADir)(m)
	require.NoError(t, err)

	hello, err := DecodeMock(m, []byte(`{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200}}`), "json")
	require.NoError(t, err)
	t.Cleanup(func() { _ = hello.Close() })

	require.Error(t, store.Store(ctx, hello))
	_, err = store.PickAll(ctx)
	require.Error(t, err)
	_, err = store.Pick(ctx, "hello")
	require.Error(t, err)
	require.Error(t, store.Delete(ctx, "hello"))
	require.Error(t, store.Clear(ctx))
	require.ErrorIs(t, store.Health(ctx), syscall.ENOTDIR)

	inTheWay := t.TempDir()

	store, err = FileStore(inTheWay)(m)
	require.NoError(t, err)

	// A directory where the mock's file goes: the write fails, and leaves no temporary file.
	require.NoError(t, os.Mkdir(filepath.Join(inTheWay, "hello.mock.yaml"), 0o700))
	require.Error(t, store.Store(ctx, hello))

	temps, err := filepath.Glob(filepath.Join(inTheWay, adminapi.FileStoreTempPattern))
	require.NoError(t, err)
	require.Empty(t, temps)
}

func TestFileStore_IsReadyBeforeItsDirectoryExists(t *testing.T) {
	// A server whose admin API has added no mock yet has no store directory: it serves, so it is ready.
	m := adminServer(t, Setup())

	store, err := FileStore(filepath.Join(t.TempDir(), "_admin"))(m)
	require.NoError(t, err)

	require.NoError(t, store.Health(t.Context()))
}

func TestEncodeMock_AMockThatCantBeDescribed(t *testing.T) {
	// A name of a mock made in Go can hold bytes that aren't UTF-8, which a description can't.
	m := adminServer(t, Setup())
	scope := m.MustMock(Getf("/x").ID("x").Name("\xff").Reply(OK()))

	_, err := EncodeMock(scope.Mock("x"), "json")
	require.ErrorContains(t, err, `describing mock "x"`)
}

// errRefused is a file system refusing to remove a file, such as one that is immutable.
var errRefused = errors.New("file system: operation not permitted")

// refusingStore makes a file store of dir whose file system refuses to remove the files named names.
func refusingStore(dir string, names ...string) AdminStore {
	return func(app *Server) (vlatte.Store[*Mock], error) {
		remove := func(name string) error {
			if slices.Contains(names, filepath.Base(name)) {
				return errRefused
			}

			return os.Remove(name)
		}

		return newFileStore(app, dir, remove), nil
	}
}

func TestFileStore_ADeleteTheFileSystemRefusesKeepsTheMock(t *testing.T) {
	// A delete whose file stays would serve again on the next reload: it fails, and the mock stays as it was, served
	// and kept.
	dir := t.TempDir()
	m := adminServer(t, Setup().AdminStore(refusingStore(dir, "hello.mock.yaml")))
	addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200}}`)

	res, body := do(t, http.MethodDelete, m.AdminURL()+"/mocks/hello", nil, nil)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))
	require.Contains(t, string(body), "operation not permitted")

	res, _ = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.FileExists(t, filepath.Join(dir, "hello.mock.yaml"))

	require.NoError(t, m.Reload())

	res, _ = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestFileStore_AClearThatFailsKeepsEveryFile(t *testing.T) {
	// The files removed before the one that fails come back: a restart serves what the server serves.
	dir := t.TempDir()
	m := adminServer(t, Setup().AdminStore(refusingStore(dir, "b.mock.yaml")))
	addMock(t, m, `{"id": "a", "request": {"method": "GET", "path": "/a"}, "response": {"status": 200, "body": "a"}}`)
	addMock(t, m, `{"id": "b", "request": {"method": "GET", "path": "/b"}, "response": {"status": 200, "body": "b"}}`)

	before, err := os.ReadFile(filepath.Join(dir, "a.mock.yaml"))
	require.NoError(t, err)

	res, body := do(t, http.MethodDelete, m.AdminURL()+"/mocks", nil, nil)
	require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))

	after, err := os.ReadFile(filepath.Join(dir, "a.mock.yaml"))
	require.NoError(t, err, "a.mock.yaml is back")
	require.Equal(t, string(before), string(after))
	require.FileExists(t, filepath.Join(dir, "b.mock.yaml"))

	for _, path := range []string{"/a", "/b"} {
		res, _ = do(t, http.MethodGet, m.URL(path), nil, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, path)
	}
}

func TestFileStore_AWriteThatFailsChangesNoFile(t *testing.T) {
	// A change of two mocks whose second file can't be written, here as a directory is in its place, leaves the first
	// file as it was.
	ctx := t.Context()
	dir := t.TempDir()
	m := adminServer(t, Setup())

	store, err := FileStore(dir)(m)
	require.NoError(t, err)

	build := func(id, body string) *Mock {
		built, err := DecodeMock(m, []byte(`{"id": "`+id+`", "request": {"method": "GET", "path": "/`+id+`"}, "response": {"status": 200, "body": "`+body+`"}}`), "json")
		require.NoError(t, err)
		t.Cleanup(func() { _ = built.Close() })

		return built
	}

	a := build("a", "first")
	require.NoError(t, store.Store(ctx, a))

	before, err := os.ReadFile(filepath.Join(dir, "a.mock.yaml"))
	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "b.mock.yaml"), 0o700))
	require.Error(t, store.Replace(ctx, []*Mock{a}, build("a", "second"), build("b", "b")))

	after, err := os.ReadFile(filepath.Join(dir, "a.mock.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestFileStore_AStaleDeleteLeavesTheNewerMock(t *testing.T) {
	// A delete picks the mock it removes, then removes it: a reload or a PUT in between swaps in a newer mock with its
	// id, which stays, with its file, as the delete only removes the mock it picked.
	for name, between := range map[string]func(t *testing.T, m *Server){
		"reload": func(t *testing.T, m *Server) { require.NoError(t, m.Reload()) },
		"put": func(t *testing.T, m *Server) {
			res, body := do(t, http.MethodPut, m.AdminURL()+"/mocks/hello", nil,
				[]byte(`{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body": "newer"}}`))
			require.Equal(t, http.StatusOK, res.StatusCode, string(body))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			m := adminServer(t, Setup().AdminStore(FileStore(dir)))
			addMock(t, m, `{"id": "hello", "request": {"method": "GET", "path": "/hello"}, "response": {"status": 200, "body": "newer"}}`)

			picked, err := m.adminStore.Pick(t.Context(), "hello")
			require.NoError(t, err)

			between(t, m)

			require.NoError(t, m.adminStore.Replace(t.Context(), []*Mock{picked}))

			require.FileExists(t, filepath.Join(dir, "hello.mock.yaml"))

			_, body := do(t, http.MethodGet, m.URL("/hello"), nil, nil)
			require.Equal(t, "newer", string(body))

			require.NoError(t, m.Reload())

			_, body = do(t, http.MethodGet, m.URL("/hello"), nil, nil)
			require.Equal(t, "newer", string(body), "its file stays")
		})
	}
}

func TestFileStore_KeepsAMockOfACustomMatcher(t *testing.T) {
	// The store writes a mock as its description and builds it back: a custom matcher, inside a built-in one, describes
	// itself as its spec, so the mock is stored, and serves the same after a restart.
	dir := t.TempDir()
	setup := func() *ConfigBuilder {
		return Setup().AdminStore(FileStore(dir)).
			Matchers(matchbuild.MustNew(matchbuild.WithMatcher("longer-than", longerThan)))
	}

	m := adminServer(t, setup())
	addMock(t, m, `{"id": "short", "request": {"method": "POST", "body": ["not", ["longer-than", 3]]}, "response": {"status": 200}}`)
	m.Close()

	m = adminServer(t, setup())

	res, _ := do(t, http.MethodPost, m.URL(), nil, []byte("abc"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = do(t, http.MethodPost, m.URL(), nil, []byte("abcd"))
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestFileStore_KeepsAMockOfACustomRequestMatcher(t *testing.T) {
	dir := t.TempDir()
	setup := func() *ConfigBuilder {
		return Setup().AdminStore(FileStore(dir)).
			Matchers(matchbuild.MustNew(matchbuild.WithMatcher("has-header", hasHeader)))
	}

	m := adminServer(t, setup())
	addMock(t, m, `{"id": "admin", "request": {"method": "GET", "custom": [["has-header", "X-Admin"]]}, "response": {"status": 200}}`)
	m.Close()

	m = adminServer(t, setup())

	res, _ := do(t, http.MethodGet, m.URL(), http.Header{"X-Admin": {"1"}}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = do(t, http.MethodGet, m.URL(), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestFileStore_KeepsAMockOfFaults(t *testing.T) {
	// Faults describe themselves as their specs: a mock with faults is stored, and has them after a restart.
	dir := t.TempDir()

	m := adminServer(t, Setup().AdminStore(FileStore(dir)))
	addMock(t, m, `{"id": "faulty", "request": {"method": "GET", "path": "/f"}, "response": {"status": 200},
		"steps": [{"type": "limit_data", "bytes": 3}, {"type": "bandwidth", "rate": "3g"}],
		"wire_steps": [{"type": "connection_reset_by_peer"}]}`)
	m.Close()

	m = adminServer(t, Setup().AdminStore(FileStore(dir)))

	res, body := do(t, http.MethodGet, m.AdminURL()+"/mocks/faulty", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, []any{
		map[string]any{"type": "limit_data", "bytes": 3.0},
		map[string]any{"type": "bandwidth", "rate": float64(fault.Bandwidth3G)},
	}, got["steps"])
	require.Equal(t, []any{map[string]any{"type": "connection_reset_by_peer"}}, got["wire_steps"])
}
