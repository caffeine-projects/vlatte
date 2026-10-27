package adminapi

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// docFileStore returns a file store of dir that keeps a docMock as a file holding its id, whose files remove removes.
func docFileStore(dir string, remove func(name string) error) *FileStore[*docMock] {
	encode := func(m *docMock) ([]byte, error) { return []byte(m.ID()), nil }
	decode := func(_ string, data []byte, _ string) (*docMock, error) {
		return newDocMock(strings.TrimSpace(string(data)), true), nil
	}

	return NewFileStore(dir, encode, decode, remove)
}

func TestFileStore_ListFailsWhenItsDirectoryCantBeRead(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	_, err := docFileStore(notADir, os.Remove).List(t.Context())
	require.ErrorIs(t, err, syscall.ENOTDIR)
}

func TestFileStore_AStoreWhoseDirectoryCantBeMadeWritesNothing(t *testing.T) {
	// The directory is a link to nothing: it holds no mock yet, and can't be made, so the mock isn't kept.
	dir := filepath.Join(t.TempDir(), "admin")
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing", "admin"), dir))

	err := docFileStore(dir, os.Remove).Store(t.Context(), newDocMock("a", true))
	require.ErrorContains(t, err, "server: admin store: ")
	require.ErrorContains(t, err, dir)
}

func TestFileStore_AFileGoneBeforeItsRemovalIsRemoved(t *testing.T) {
	// A person removed the file between the store reading its directory and removing it: the delete did what it
	// asked for.
	dir := t.TempDir()
	store := docFileStore(dir, func(name string) error {
		require.NoError(t, os.Remove(name))
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	})
	require.NoError(t, store.Store(t.Context(), newDocMock("a", true)))

	require.NoError(t, store.Delete(t.Context(), "a"))
	require.NoFileExists(t, filepath.Join(dir, "a.mock.yaml"))
}

func TestFileStore_AFileThatCantBeReadFailsItsPick(t *testing.T) {
	// A link to nothing reads as a mock file, but holds no mock.
	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "a.mock.yaml")))

	_, err := docFileStore(dir, os.Remove).Pick(t.Context(), "a")
	require.ErrorContains(t, err, "server: admin store: ")
	require.ErrorIs(t, err, fs.ErrNotExist)
}
