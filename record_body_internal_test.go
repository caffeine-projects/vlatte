package vlatte

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func newTestDir(t *testing.T, maxInline, maxBody int64) *recordDir {
	t.Helper()

	nop := zerolog.Nop()

	return &recordDir{
		dir:       t.TempDir(),
		maxInline: maxInline,
		maxBody:   maxBody,
		logger:    &nop,
		live:      make(map[*RecordBody]struct{}),
	}
}

// slowFile is a disk that writes nothing until released.
type slowFile struct {
	*os.File

	release chan struct{}
}

func (f slowFile) Write(p []byte) (int, error) {
	<-f.release
	return f.File.Write(p)
}

func TestRecordBody_DropsWhenTheDiskFallsBehind(t *testing.T) {
	// #33: a response must not wait for its recording. When the disk falls behind the body, the body is dropped,
	// and Keep returns at once all along: the sender never waits for the disk.
	release := make(chan struct{})
	opened := make(chan string, 1)

	open := _openSpool
	_openSpool = func(dir string) (spoolFile, error) {
		f, err := os.CreateTemp(dir, _tempPattern)
		if err != nil {
			return nil, err
		}

		opened <- f.Name()

		return slowFile{File: f, release: release}, nil
	}

	t.Cleanup(func() { _openSpool = open })

	d := newTestDir(t, 1024, 1<<40)
	b := d.newBody()

	chunk := make([]byte, 64<<10)
	start := time.Now()

	for range 2 * (_spoolQueue * _spoolChunk) / len(chunk) {
		b.Keep(chunk)
	}

	require.Less(t, time.Since(start), time.Second, "Keep waited for the disk")
	require.ErrorIs(t, b.Err(), errDiskTooSlow)

	close(release)
	b.Close()
	d.spools.Wait()

	require.NoFileExists(t, <-opened, "the file of a dropped body is removed")
}

func TestRecordBody_FileThatCantBeCreated(t *testing.T) {
	d := newTestDir(t, 8, 1<<40)
	d.dir = filepath.Join(d.dir, "missing")

	b := d.newBody()
	b.Keep([]byte("more than eight bytes"))
	b.Close()
	d.spools.Wait()

	files := &RecordFiles{dir: d, slug: "x"}
	_, err := files.PlaceSpooled(b, ".bin")
	require.Error(t, err)
}

func TestRecordBody_States(t *testing.T) {
	t.Run("nothing is kept after Close", func(t *testing.T) {
		d := newTestDir(t, 1024, 1<<40)
		b := d.newBody()
		b.Keep([]byte("a"))
		b.Close()
		b.Close()
		b.Keep([]byte("b"))

		data, ok := b.Bytes()
		require.True(t, ok)
		require.Equal(t, "a", string(data))
		require.Equal(t, int64(1), b.Len())
	})

	t.Run("an open body has no bytes yet, and can't be placed", func(t *testing.T) {
		d := newTestDir(t, 4, 1<<40)
		b := d.newBody()
		b.Keep([]byte("spooled"))

		_, ok := b.Bytes()
		require.False(t, ok)

		_, err := (&RecordFiles{dir: d, slug: "x"}).PlaceSpooled(b, ".bin")
		require.ErrorIs(t, err, errBodyOpen)

		b.Close()
		d.spools.Wait()
	})

	t.Run("a body in memory is placed from its bytes", func(t *testing.T) {
		d := newTestDir(t, 1024, 1<<40)
		b := d.newBody()
		b.Keep([]byte("a"))
		b.Close()

		_, err := (&RecordFiles{dir: d, slug: "x"}).PlaceSpooled(b, ".bin")
		require.ErrorIs(t, err, errBodyInMemory)
	})

	t.Run("a placed body is the recording's", func(t *testing.T) {
		d := newTestDir(t, 4, 1<<40)
		b := d.newBody()
		b.Keep([]byte("spooled body"))
		b.Close()

		files := &RecordFiles{dir: d, slug: "x"}
		name, err := files.PlaceSpooled(b, ".bin")
		require.NoError(t, err)

		_, err = files.PlaceSpooled(b, ".bin")
		require.ErrorIs(t, err, errBodyPlaced, "placed twice, the file would be removed under the first")

		b.Discard()
		require.NoError(t, b.Err())
		require.FileExists(t, files.pending[0].temp, "a discard after the placing leaves the file to the recording")
		require.Equal(t, name, files.pending[0].name)

		files.discard()
		require.NoFileExists(t, filepath.Join(d.dir, name))
	})
}

func TestRecordFiles_SharesEqualBodies(t *testing.T) {
	d := newTestDir(t, 4, 1<<40)
	files := &RecordFiles{dir: d, slug: "x"}

	a, _ := files.Place([]byte("same content"), true, ".txt")
	b, _ := files.Place([]byte("same content"), true, ".txt")
	c, _ := files.Place([]byte("other content"), true, ".txt")

	require.Equal(t, a, b)
	require.NotEqual(t, a, c)
	require.Len(t, files.pending, 2)
}

func TestCleanExt(t *testing.T) {
	tests := map[string]string{
		".json":        ".json",
		".PNG":         ".PNG",
		"json":         ".bin",
		"":             ".bin",
		".":            ".bin",
		"./etc":        ".bin",
		".a/b":         ".bin",
		".tar.gz":      ".bin",
		".verylongx":   ".verylongx",
		".elevenchars": ".bin",
	}

	for ext, want := range tests {
		require.Equal(t, want, cleanExt(ext), ext)
	}
}

func FuzzRecordSlug(f *testing.F) {
	// Whatever a mock's name, its slug is a safe part of a file name on every system: short, never empty or hidden,
	// and the same for the same name.
	for _, seed := range []string{"GET /items?page=2", "../../etc/passwd", "", "   ", ".hidden", "café", strings.Repeat("a", 200)} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		slug := recordSlug(name)

		require.NotEmpty(t, slug)
		require.LessOrEqual(t, len(slug), _slugMax)
		require.Equal(t, slug, recordSlug(name))
		require.True(t, utf8.ValidString(slug))
		require.False(t, strings.HasPrefix(slug, "."), slug)
		require.False(t, strings.HasPrefix(slug, "-"), slug)

		for i := range len(slug) {
			c := slug[i]
			require.True(t, isAlnum(c) || c == '.' || c == '_' || c == '-', "%q in %q", c, slug)
		}
	})
}
