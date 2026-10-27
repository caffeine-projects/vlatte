package atomicfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/atomicfile"
)

func TestWrite(t *testing.T) {
	// The file holds the data, readable by others, as a mock file must be for the servers that load it.
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mock.json")

	require.NoError(t, atomicfile.Write(dir, ".tmp-*", path, []byte("{}")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWrite_LeavesNoTemporaryFile(t *testing.T) {
	// A write that fails, here because a directory is in the way, removes the temporary file it wrote.
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mock.json")
	require.NoError(t, os.MkdirAll(filepath.Join(path, "in the way"), 0o755))

	require.Error(t, atomicfile.Write(dir, ".tmp-*", path, []byte("{}")))

	temps, err := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	require.NoError(t, err)
	require.Empty(t, temps)
}

func TestWritePerm(t *testing.T) {
	// A file of requests, which may hold secrets, is its user's alone.
	dir := t.TempDir()
	path := filepath.Join(dir, "a.idx")

	require.NoError(t, atomicfile.WritePerm(dir, ".tmp-*", path, []byte("{}"), 0o600))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
