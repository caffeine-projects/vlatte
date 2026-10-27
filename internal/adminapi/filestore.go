package adminapi

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
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var _ vlatte.Store[vlatte.Mock] = (*FileStore[vlatte.Mock])(nil)

const (
	// FileStoreFormat is the format of the mock files a FileStore writes.
	FileStoreFormat = "yaml"

	// FileStoreTempPattern names the files a FileStore is writing: never *.mock.*, so reading the store skips them.
	FileStoreTempPattern = ".admin-*.tmp"
)

// FileStore keeps the mocks of the admin API as mock files, <id>.mock.yaml in a directory: a persistent store of a
// mock server's admin mocks. Its changes are serialized, and change its files all or nothing. Pick, PickAll and List
// build new mocks from the files each time. A mock whose description doesn't load back is refused: its file would
// fail every later start.
type FileStore[M vlatte.Mock] struct {
	dir    string
	encode func(mock M) ([]byte, error)
	decode func(origin string, data []byte, format string) (M, error)
	remove func(name string) error
	mu     sync.Mutex
}

// fileWrite is a file a change of the store writes.
type fileWrite struct {
	path string
	data []byte
}

// NewFileStore returns the store of the mock files of dir. encode describes a mock as its file holds it, in
// FileStoreFormat. decode builds a mock the admin API added from data, a mock file in format: origin is where its
// problems point, such as "file <path>". remove removes a file: os.Remove, or one that fails, as a file system can.
func NewFileStore[M vlatte.Mock](
	dir string,
	encode func(mock M) ([]byte, error),
	decode func(origin string, data []byte, format string) (M, error),
	remove func(name string) error,
) *FileStore[M] {
	return &FileStore[M]{dir: dir, encode: encode, decode: decode, remove: remove}
}

// List returns the enabled mocks of the files.
func (s *FileStore[M]) List(ctx context.Context) (iter.Seq[M], error) {
	all, err := s.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	// The mocks it leaves out are built for nothing: they release what they hold.
	return slices.Values(slices.DeleteFunc(all, func(m M) bool {
		if m.Enabled() {
			return false
		}

		closeMocks([]M{m})

		return true
	})), nil
}

// Store writes the files of mocks.
func (s *FileStore[M]) Store(ctx context.Context, mocks ...M) error {
	return s.Replace(ctx, nil, mocks...)
}

// Replace writes the files of mocks, then removes those of the mocks of old it doesn't write again. Every mock is
// described and checked first: when one fails, nothing is written. When a file can't be written or removed, the files
// it changed are put back.
func (s *FileStore[M]) Replace(_ context.Context, old []M, mocks ...M) error {
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

		data, err := s.check(mock)
		if err != nil {
			return err
		}

		written[mock.ID()] = struct{}{}
		docs = append(docs, data)
	}

	writes := make([]fileWrite, 0, len(mocks))

	var removes []string

	for i, mock := range mocks {
		path := filepath.Join(s.dir, mock.ID()+".mock."+FileStoreFormat)
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

// Pick builds the mock of id from its file.
func (s *FileStore[M]) Pick(_ context.Context, id string) (M, error) {
	held, err := s.files()
	if err != nil {
		var zero M
		return zero, err
	}

	path, ok := held[id]
	if !ok {
		var zero M
		return zero, vlatte.ErrNotFound
	}

	return s.load(path)
}

// PickAll builds the mocks of every file, in file name order. When a file has problems, it builds none, and reports
// every file with problems.
func (s *FileStore[M]) PickAll(context.Context) ([]M, error) {
	held, err := s.files()
	if err != nil {
		return nil, err
	}

	paths := slices.Sorted(maps.Values(held))

	var errs []error

	mocks := make([]M, 0, len(paths))
	for _, path := range paths {
		mock, err := s.load(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		mocks = append(mocks, mock)
	}

	if len(errs) > 0 {
		closeMocks(mocks)
		return nil, errors.Join(errs...)
	}

	return mocks, nil
}

// Delete removes the files of the mocks of ids.
func (s *FileStore[M]) Delete(_ context.Context, ids ...string) error {
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

// Clear removes every file of the store.
func (s *FileStore[M]) Clear(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	held, err := s.files()
	if err != nil {
		return err
	}

	return s.apply(nil, slices.Sorted(maps.Values(held)))
}

// Health returns an error when the store's directory can't be read. A directory that doesn't exist yet is ready: the
// first mock the store keeps makes it.
func (s *FileStore[M]) Health(context.Context) error {
	_, err := s.files()
	return err
}

// apply writes and then removes files, all or nothing: on the first failure, it puts back the files it changed, and
// returns the failure, with what it couldn't put back. A file already gone is removed.
func (s *FileStore[M]) apply(writes []fileWrite, removes []string) error {
	changed := make([]atomicfile.Saved, 0, len(writes)+len(removes))

	fail := func(err error) error {
		return fmt.Errorf("server: admin store: %w", errors.Join(err, atomicfile.Restore(changed, FileStoreTempPattern)))
	}

	for _, w := range writes {
		file, err := atomicfile.Backup(w.path)
		if err == nil {
			err = atomicfile.Write(s.dir, FileStoreTempPattern, w.path, w.data)
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

// files returns the mock files of the store's directory, <id>.mock.<format>, by the ID of their mock, as their names
// give it. A directory that doesn't exist holds none.
func (s *FileStore[M]) files() (map[string]string, error) {
	entries, err := os.ReadDir(s.dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return make(map[string]string), nil
	case err != nil:
		return nil, fmt.Errorf("server: admin store: %w", err)
	}

	held := make(map[string]string)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		for _, format := range config.Formats() {
			if id, ok := strings.CutSuffix(e.Name(), ".mock."+format); ok {
				held[id] = filepath.Join(s.dir, e.Name())
				break
			}
		}
	}

	return held, nil
}

// check describes mock as its file holds it, and checks that the file loads back.
func (s *FileStore[M]) check(mock M) ([]byte, error) {
	data, err := s.encode(mock)
	if err != nil {
		return nil, err
	}

	built, err := s.decode("admin request", data, FileStoreFormat)
	if err != nil {
		return nil, fmt.Errorf("server: mock %q can't be stored: its description doesn't load back: %w", mock.ID(), err)
	}

	closeMocks([]M{built})

	return data, nil
}

// load builds the mock of the file at path.
func (s *FileStore[M]) load(path string) (M, error) {
	format, _ := config.Format(filepath.Ext(path))

	data, err := os.ReadFile(path)
	if err != nil {
		var zero M
		return zero, fmt.Errorf("server: admin store: %w", err)
	}

	return s.decode("file "+path, data, format)
}
