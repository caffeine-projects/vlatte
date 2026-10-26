package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var _ vlatte.Store[*Mock] = (*fileStore)(nil)

const (
	// _fileStoreFormat is the format of the mock files a FileStore writes.
	_fileStoreFormat = "yaml"

	// _fileStoreTempPattern names the files a FileStore is writing: never *.mock.*, so reading the store skips them.
	_fileStoreTempPattern = ".admin-*.tmp"
)

// EncodeMock describes mock in format, json, yaml or yml, as the admin API's GET /mocks/{id} does: what a persistent
// store of the admin API keeps, such as a document of a database. DecodeMock builds the mock back. Relative body files
// are named from the working directory. A mock that can't be described fails.
func EncodeMock(mock *Mock, format string) ([]byte, error) {
	f, ok := mockFileFormat(format)
	if !ok {
		return nil, fmt.Errorf("server: format %q is not one of %s", format, strings.Join(MockFileFormats(), ", "))
	}

	doc, err := value.ViaJSON(mock.Describe())
	if err != nil {
		return nil, fmt.Errorf("server: describing mock %q: %w", mock.ID(), err)
	}

	return config.Encode(doc, f)
}

// DecodeMock builds the mock data describes in format, as EncodeMock wrote it, for the server app, as a mock the admin
// API added: a reset of the server removes it, unless its store holds it still. Its relative body files open from the
// working directory. When data has problems, the error lists each.
func DecodeMock(app *Server, data []byte, format string) (*Mock, error) {
	return buildAdminMock(app, data, format)
}

// FileStore makes a persistent store of the mocks the admin API adds: a mock file each, <id>.mock.yaml in dir, as
// EncodeMock describes the mock. Only the store reads them: keep dir out of the mock file patterns, or its mocks would
// load twice. A relative dir starts at the server's root directory, and WorkDirVar at its work directory, as in
// "{workdir}/_admin". A file a person adds, edits or removes takes effect
// on the server's next reload. A mock whose description doesn't load back is refused: its file would fail every later
// start.
func FileStore(dir string) AdminStore {
	return func(app *Server) (vlatte.Store[*Mock], error) {
		return &fileStore{app: app, dir: app.config.path(dir), remove: os.Remove}, nil
	}
}

// fileStore keeps the mocks of the admin API as mock files in dir. Its changes are serialized, and change its files
// all or nothing. Pick, PickAll and List build new mocks from the files each time.
type fileStore struct {
	app    *Server
	dir    string
	remove func(name string) error // os.Remove; tests make it fail, as a file system can
	mu     sync.Mutex
}

// fileWrite is a file a change of the store writes.
type fileWrite struct {
	path string
	data []byte
}

func (s *fileStore) List(ctx context.Context) (iter.Seq[*Mock], error) {
	all, err := s.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	// The mocks it leaves out are built for nothing: they release what they hold.
	return slices.Values(slices.DeleteFunc(all, func(m *Mock) bool {
		if m.Enabled() {
			return false
		}

		_ = m.Close()

		return true
	})), nil
}

func (s *fileStore) Store(ctx context.Context, mocks ...*Mock) error {
	return s.Replace(ctx, nil, mocks...)
}

// Replace writes the files of mocks, then removes those of the mocks of old it doesn't write again. Every mock is
// described and checked first: when one fails, nothing is written. When a file can't be written or removed, the files
// it changed are put back.
func (s *fileStore) Replace(_ context.Context, old []*Mock, mocks ...*Mock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, err := s.files()
	if err != nil {
		return err
	}

	gone := make(map[string]struct{}, len(old))
	for _, mock := range old {
		gone[mock.ID()] = struct{}{}
	}

	written := make(map[string]struct{}, len(mocks))
	docs := make([][]byte, 0, len(mocks))

	for _, mock := range mocks {
		_, taken := held[mock.ID()]
		_, replaced := gone[mock.ID()]
		_, repeated := written[mock.ID()]

		if taken && !replaced || repeated {
			return fmt.Errorf("%w: %q", vlatte.ErrDuplicateID, mock.ID())
		}

		data, err := s.encode(mock)
		if err != nil {
			return err
		}

		written[mock.ID()] = struct{}{}
		docs = append(docs, data)
	}

	writes := make([]fileWrite, 0, len(mocks))

	var removes []string

	for i, mock := range mocks {
		path := filepath.Join(s.dir, mock.ID()+".mock."+_fileStoreFormat)
		writes = append(writes, fileWrite{path: path, data: docs[i]})

		// A file of the mock in another format, such as one a person wrote, is replaced too.
		if prev, ok := held[mock.ID()]; ok && prev != path {
			removes = append(removes, prev)
		}
	}

	for id := range gone {
		if _, again := written[id]; !again {
			if path, ok := held[id]; ok {
				removes = append(removes, path)
			}
		}
	}

	if len(writes) > 0 {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			return fmt.Errorf("server: admin store: %w", err)
		}
	}

	return s.apply(writes, removes)
}

func (s *fileStore) Pick(_ context.Context, id string) (*Mock, error) {
	held, err := s.files()
	if err != nil {
		return nil, err
	}

	path, ok := held[id]
	if !ok {
		return nil, vlatte.ErrNotFound
	}

	return s.decode(path)
}

// PickAll builds the mocks of every file, in file name order. When a file has problems, it builds none, and reports
// every file with problems.
func (s *fileStore) PickAll(context.Context) ([]*Mock, error) {
	held, err := s.files()
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(held))
	for _, path := range held {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	var errs []error

	mocks := make([]*Mock, 0, len(paths))
	for _, path := range paths {
		mock, err := s.decode(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		mocks = append(mocks, mock)
	}

	if len(errs) > 0 {
		for _, mock := range mocks {
			_ = mock.Close()
		}

		return nil, errors.Join(errs...)
	}

	return mocks, nil
}

func (s *fileStore) Delete(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, err := s.files()
	if err != nil {
		return err
	}

	var removes []string

	for _, id := range ids {
		if path, ok := held[id]; ok {
			removes = append(removes, path)
		}
	}

	return s.apply(nil, removes)
}

func (s *fileStore) Clear(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, err := s.files()
	if err != nil {
		return err
	}

	return s.apply(nil, slices.Sorted(maps.Values(held)))
}

// apply writes and then removes files, all or nothing: on the first failure, it puts back the files it changed, and
// returns the failure, with what it couldn't put back. A file already gone is removed.
func (s *fileStore) apply(writes []fileWrite, removes []string) error {
	changed := make([]atomicfile.Saved, 0, len(writes)+len(removes))

	fail := func(err error) error {
		return fmt.Errorf("server: admin store: %w", errors.Join(err, atomicfile.Restore(changed, _fileStoreTempPattern)))
	}

	for _, w := range writes {
		file, err := atomicfile.Backup(w.path)
		if err == nil {
			err = atomicfile.Write(s.dir, _fileStoreTempPattern, w.path, w.data)
		}

		if err != nil {
			return fail(err)
		}

		changed = append(changed, file)
	}

	for _, path := range removes {
		file, err := atomicfile.Backup(path)
		if err == nil {
			if err = s.remove(path); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		}

		if err != nil {
			return fail(err)
		}

		changed = append(changed, file)
	}

	return nil
}

// files returns the mock files of the store's directory by the ID of their mock, as their names give it.
func (s *fileStore) files() (map[string]string, error) {
	held := make(map[string]string)

	err := mockFilesIn(s.dir, func(file string) {
		name := filepath.Base(file)
		held[name[:strings.LastIndex(name, ".mock.")]] = file
	})
	if err != nil {
		return nil, fmt.Errorf("server: admin store: %w", err)
	}

	return held, nil
}

// encode describes mock as its file holds it, and checks that the file loads back.
func (s *fileStore) encode(mock *Mock) ([]byte, error) {
	data, err := EncodeMock(mock, _fileStoreFormat)
	if err != nil {
		return nil, err
	}

	built, err := DecodeMock(s.app, data, _fileStoreFormat)
	if err != nil {
		return nil, fmt.Errorf("server: mock %q can't be stored: its description doesn't load back: %w", mock.ID(), err)
	}

	_ = built.Close()

	return data, nil
}

// decode builds the mock of the file at path, as one the admin API added. Its problems point at the file.
func (s *fileStore) decode(path string) (*Mock, error) {
	format, _ := mockFileFormat(filepath.Ext(path))

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("server: admin store: %w", err)
	}

	return mockDoc{origin: "file " + path, source: adminapi.Origin, format: format, data: data}.build(s.app)
}
