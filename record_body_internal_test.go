package vlatte

import (
	"os"
	"path/filepath"
	"slices"
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
		done:      make(chan struct{}),
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

// spoolOn has the bodies of the test spool to files that open opens.
func spoolOn(t *testing.T, open func(dir string) (spoolFile, error)) {
	t.Helper()

	before := _openSpool
	_openSpool = open

	t.Cleanup(func() { _openSpool = before })
}

// waitAtMost has the bodies of the test wait d at most for the disk.
func waitAtMost(t *testing.T, d time.Duration) {
	t.Helper()

	before := _spoolWait
	_spoolWait = d

	t.Cleanup(func() { _spoolWait = before })
}

func TestRecordBody_DropsWhenTheDiskFallsBehind(t *testing.T) {
	// #33: a response waits for its recording a bounded time only. A disk that falls further behind the body than
	// _spoolWait drops it, and the sender goes on at its own pace.
	release := make(chan struct{})
	opened := make(chan string, 1)

	spoolOn(t, func(dir string) (spoolFile, error) {
		f, err := os.CreateTemp(dir, _tempPattern)
		if err != nil {
			return nil, err
		}

		opened <- f.Name()

		return slowFile{File: f, release: release}, nil
	})

	waitAtMost(t, 50*time.Millisecond)

	d := newTestDir(t, 1024, 1<<40)
	b := d.newBody()

	chunk := make([]byte, 64<<10)
	start := time.Now()

	for range 2 * (_spoolQueue * _spoolChunk) / len(chunk) {
		b.Keep(chunk)
	}

	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, _spoolWait, "the sender waited for the disk")
	require.Less(t, elapsed, _spoolWait+2*time.Second, "the sender waited longer than _spoolWait")
	require.ErrorIs(t, b.Err(), errDiskTooSlow)

	close(release)
	b.Close()
	d.spools.Wait()

	require.NoFileExists(t, <-opened, "the file of a dropped body is removed")
}

// pacedFile is a disk that takes a while for each write.
type pacedFile struct {
	*os.File

	pace time.Duration
}

func (f pacedFile) Write(p []byte) (int, error) {
	time.Sleep(f.pace)
	return f.File.Write(p)
}

func TestRecordBody_WaitsForASlowDisk(t *testing.T) {
	// A sender faster than the disk goes at the disk's pace: the body is kept whole.
	spoolOn(t, func(dir string) (spoolFile, error) {
		f, err := os.CreateTemp(dir, _tempPattern)
		if err != nil {
			return nil, err
		}

		return pacedFile{File: f, pace: time.Millisecond}, nil
	})

	d := newTestDir(t, 1024, 1<<40)
	b := d.newBody()

	content := make([]byte, 4*_spoolQueue*_spoolChunk)
	for i := range content {
		content[i] = byte(i)
	}

	for chunk := range slices.Chunk(content, 16<<10) {
		b.Keep(chunk)
	}

	b.Close()
	require.NoError(t, b.Err())

	files := &RecordFiles{dir: d, slug: "x"}
	name, err := files.PlaceSpooled(b, ".bin")
	require.NoError(t, err)

	kept, err := os.ReadFile(files.pending[0].temp)
	require.NoError(t, err)
	require.Equal(t, content, kept)

	files.discard()
	require.NoFileExists(t, filepath.Join(d.dir, name))
}

func TestRecordBody_StopReleasesABodyWaitingForTheDisk(t *testing.T) {
	// A recording that stops doesn't wait for a body that waits for the disk: the body is dropped, and its sender
	// goes on.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	spoolOn(t, func(dir string) (spoolFile, error) {
		f, err := os.CreateTemp(dir, _tempPattern)
		if err != nil {
			return nil, err
		}

		return slowFile{File: f, release: release}, nil
	})

	waitAtMost(t, time.Minute)

	d := newTestDir(t, 1024, 1<<40)
	b := d.newBody()

	sent := make(chan struct{})

	go func() {
		defer close(sent)

		chunk := make([]byte, 64<<10)
		for range 2 * (_spoolQueue * _spoolChunk) / len(chunk) {
			b.Keep(chunk)
		}
	}()

	// The queue holds 2 MiB: the sender fills it at once, then waits for the disk.
	time.Sleep(100 * time.Millisecond)

	d.stop()

	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the sender still waits for the disk")
	}

	require.ErrorIs(t, b.Err(), errBodyStopped)
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

// bodyCall is a call of a protocol whose answer is a body only.
type bodyCall struct {
	body *RecordBody
}

type bodyCodec struct{}

func (bodyCodec) Name(bodyCall) string { return "GET /a" }

func (bodyCodec) Request(bodyCall, *RecordFiles) (string, error) { return "a", nil }

func (bodyCodec) Response(x bodyCall, _ *RecordFiles) (string, error) {
	if err := x.body.Err(); err != nil {
		return "", err
	}

	b, _ := x.body.Bytes()

	return string(b), nil
}

func (bodyCodec) Mock(m *RecordedMock[string, string]) any {
	return map[string]any{"id": m.ID, "responses": m.Responses}
}

func TestRecordSession_CallsTheStopCaughtArentFailures(t *testing.T) {
	// Stop drops the bodies still being captured, then stops taking calls: a call that ends in between is taken, and
	// isn't recorded, as Stop says. It didn't fail.
	s := MustNewRecordSession[bodyCall](bodyCodec{}, RecordOptions[string, string]{Dir: t.TempDir(), Format: "json"})

	body := s.NewBody()
	body.Keep([]byte("partial"))

	s.files.stop() // what Stop does first
	body.Close()
	require.True(t, s.Record(bodyCall{body: body}, 1), "the recording still takes calls")

	res, err := s.Stop(t.Context())
	require.NoError(t, err)
	require.Zero(t, res.Status.Failed)
	require.Empty(t, res.Recordings)
}

func TestWriteAtomic_LeavesNoTemporaryFile(t *testing.T) {
	// A write that fails, here because a directory is in the way, removes the temporary file it wrote.
	dir := t.TempDir()
	target := filepath.Join(dir, "a.mock.json")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "in the way"), 0o755))

	require.Error(t, writeAtomic(dir, target, []byte("{}")))

	temps, err := filepath.Glob(filepath.Join(dir, _tempPattern))
	require.NoError(t, err)
	require.Empty(t, temps)
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
