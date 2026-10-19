package mockhttp

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

// mockFileSpec is a mock file of a directory model.
type mockFileSpec struct {
	priority int
	id       string
	bad      bool
}

// writeMockFile writes the mock file of spec, in the format of its name, answering /f with its own name.
func writeMockFile(t require.TestingT, dir, name string, spec mockFileSpec) {
	doc := map[string]any{
		"request":  map[string]any{"path": "/f"},
		"priority": int64(spec.priority),
		"response": map[string]any{"body": name},
	}

	if spec.id != "" {
		doc["id"] = spec.id
	}

	if spec.bad {
		doc = map[string]any{"request": map[string]any{"path": "/f"}, "response": map[string]any{"stauts": int64(200)}}
	}

	format, ok := mockFileFormat(filepath.Ext(name))
	require.True(t, ok, name)

	b, err := config.Encode(doc, format)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o600))
}

func get(t require.TestingT, m *Server, path string) (int, string) {
	res, err := http.Get(m.URL() + path)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode, string(b)
}

// getFull is get, plus the response header.
func getFull(t require.TestingT, m *Server, path string) (int, http.Header, string) {
	res, err := http.Get(m.URL() + path)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode, res.Header, string(b)
}

func TestFileLoader_LoadsInNameOrder(t *testing.T) {
	// F16: mocks of equal priority match in file name order, not in the random order of a map.
	dir := t.TempDir()
	for _, name := range []string{"c.mock.yaml", "a.mock.yaml", "b.mock.json"} {
		writeMockFile(t, dir, name, mockFileSpec{})
	}

	for range 10 {
		m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
		m.MustStart()

		_, body := get(t, m, "/f")
		require.Equal(t, "a.mock.yaml", body)

		m.Close()
	}
}

func TestFileLoader_RootDirStartsRelativePatternsOnly(t *testing.T) {
	// #20: root_dir was joined to every pattern, so an absolute pattern pointed under root_dir and loaded nothing,
	// silently. An absolute pattern names its files as written; root_dir starts the relative ones.
	dir := t.TempDir()
	other := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{})

	tests := []struct {
		name    string
		rootDir string
		pattern string
		want    int
	}{
		{
			name:    "absolute pattern",
			rootDir: "",
			pattern: filepath.Join(dir, "*.mock.yaml"),
			want:    http.StatusOK,
		},
		{
			name:    "absolute pattern, root_dir elsewhere",
			rootDir: other,
			pattern: filepath.Join(dir, "*.mock.yaml"),
			want:    http.StatusOK,
		},
		{
			name:    "relative pattern under root_dir",
			rootDir: dir,
			pattern: "*.mock.yaml",
			want:    http.StatusOK,
		},
		{
			name:    "relative pattern, root_dir without the file",
			rootDir: other,
			pattern: "*.mock.yaml",
			want:    http.StatusTeapot,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := MustNew(Setup().Silent().RootDir(tc.rootDir).MockFilePatterns(tc.pattern))
			m.MustStart()
			t.Cleanup(m.Close)

			status, _ := get(t, m, "/f")
			require.Equal(t, tc.want, status)
		})
	}
}

func TestFileLoader_LoadsAFileOnceWhicheverPatternsNameIt(t *testing.T) {
	// A relative pattern and an absolute one name the same file under two paths. It loads once: twice, its ID would
	// be in use, and the load would fail.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "a"})
	t.Chdir(dir)

	m := MustNew(Setup().Silent().MockFilePatterns("*.mock.yaml", filepath.Join(dir, "*.mock.yaml")))
	require.NoError(t, m.Start())
	t.Cleanup(m.Close)

	all, err := m.storage.PickAll(context.Background())
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestFileLoader_ReportsEveryBadFile(t *testing.T) {
	// A load fixes every file in one pass, and adds nothing until all of them are fixed.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{})
	writeMockFile(t, dir, "b.mock.yaml", mockFileSpec{bad: true})
	writeMockFile(t, dir, "c.mock.json", mockFileSpec{bad: true})

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
	err := m.Start()
	t.Cleanup(m.Close)

	require.ErrorContains(t, err, "file "+filepath.Join(dir, "b.mock.yaml")+": unknown_key: response.stauts")
	require.ErrorContains(t, err, "file "+filepath.Join(dir, "c.mock.json")+": unknown_key: response.stauts")

	all, _ := m.storage.PickAll(context.Background())
	require.Empty(t, all)
}

func TestFileLoader_ReloadReplacesFileMocks(t *testing.T) {
	// F16: a reload applies edits and removals, adds no copies, and keeps mocks added in code.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "same"})

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/code").Reply(OK().PlainText("code")))

	for range 3 {
		require.NoError(t, m.Reload(), "an id is free again once its file mock is replaced")
	}

	all, _ := m.storage.PickAll(context.Background())
	require.Len(t, all, 2)

	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{priority: 5})
	writeMockFile(t, dir, "0.mock.json", mockFileSpec{priority: 1})
	require.NoError(t, m.Reload())

	_, body := get(t, m, "/f")
	require.Equal(t, "0.mock.json", body)

	require.NoError(t, os.Remove(filepath.Join(dir, "0.mock.json")))
	require.NoError(t, m.Reload())

	_, body = get(t, m, "/f")
	require.Equal(t, "a.mock.yaml", body)

	_, body = get(t, m, "/code")
	require.Equal(t, "code", body)
}

func TestFileLoaderMachine(t *testing.T) {
	// M13: whatever is done to the mock files, a reload leaves exactly the mocks of the files on disk,
	// matching in priority then name order; a failed one names every bad file and changes nothing, so the mocks
	// of the last good load still serve; mocks added in code are never touched.
	t.Parallel()

	names := []string{"a.mock.yaml", "b.mock.json", "c.mock.yaml", "d.mock.yml"}
	ids := []string{"", "", "x", "y"}

	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "loader")
		require.NoError(t, err)

		defer os.RemoveAll(dir)

		m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
		m.MustStart()

		defer m.Close()

		code := m.MustMock(Getf("/code").Reply(OK().PlainText("code")))

		files := make(map[string]mockFileSpec)
		live := make(map[string]mockFileSpec) // the files of the last good load

		t.Repeat(map[string]func(*rapid.T){
			"write": func(t *rapid.T) {
				name := rapid.SampledFrom(names).Draw(t, "name")
				spec := mockFileSpec{priority: rapid.IntRange(0, 2).Draw(t, "priority"), id: rapid.SampledFrom(ids).Draw(t, "id")}
				writeMockFile(t, dir, name, spec)
				files[name] = spec
			},
			"break": func(t *rapid.T) {
				name := rapid.SampledFrom(names).Draw(t, "name")
				writeMockFile(t, dir, name, mockFileSpec{bad: true})
				files[name] = mockFileSpec{bad: true}
			},
			"remove": func(t *rapid.T) {
				if len(files) == 0 {
					t.Skip("no file")
				}

				name := rapid.SampledFrom(slices.Sorted(maps.Keys(files))).Draw(t, "name")
				require.NoError(t, os.Remove(filepath.Join(dir, name)))
				delete(files, name)
			},
			"delete from code": func(t *rapid.T) {
				// A scope deletes only its own mocks: the file mocks are the loader's.
				id := rapid.SampledFrom(ids).Draw(t, "id")
				require.NoError(t, code.Delete(context.Background(), id))
			},
			"reload": func(t *rapid.T) {
				err := m.Reload()

				var bad []string
				taken := make(map[string]bool)
				duplicate := false

				for _, name := range slices.Sorted(maps.Keys(files)) {
					spec := files[name]
					if spec.bad {
						bad = append(bad, name)
						continue
					}

					duplicate = duplicate || (spec.id != "" && taken[spec.id])
					taken[spec.id] = spec.id != ""
				}

				switch {
				case len(bad) > 0:
					for _, name := range bad {
						require.ErrorContains(t, err, "file "+filepath.Join(dir, name)+":")
					}
				case duplicate:
					require.ErrorIs(t, err, vlatte.ErrDuplicateID)
				default:
					require.NoError(t, err)

					live = maps.Clone(files)
				}

				all, _ := m.storage.PickAll(context.Background())
				require.Len(t, all, 1+len(live))

				var origins []string
				for _, mock := range all {
					if mock.Origin() != "" {
						origins = append(origins, filepath.Base(mock.Origin()))
					}
				}

				require.ElementsMatch(t, slices.Collect(maps.Keys(live)), origins)

				status, body := get(t, m, "/f")
				if len(live) == 0 {
					require.Equal(t, StatusNoMatch, status)
				} else {
					// The lowest priority wins; then the first name.
					want := slices.MinFunc(slices.Sorted(maps.Keys(live)), func(a, b string) int { return live[a].priority - live[b].priority })
					require.Equal(t, want, body)
				}

				_, body = get(t, m, "/code")
				require.Equal(t, "code", body)
			},
			"": func(t *rapid.T) {
				all, _ := m.storage.PickAll(context.Background())
				require.Len(t, all, 1+len(live), "the code mock and the file mocks of the last good load")

				// Reloads leave no scope behind that the assertions would count.
				require.Equal(t, len(m.ListCalled()) == 0, m.AssertNotCalled(testmock.NewFakeT()))
			},
		})
	})
}

func TestFileLoader_BadPattern(t *testing.T) {
	m := MustNew(Setup().Silent().MockFilePatterns("["))

	err := m.Start()
	t.Cleanup(m.Close)

	require.ErrorContains(t, err, "loader: pattern [: syntax error in pattern")
}

func TestFileLoader_ReloadKeepsAssertNotCalled(t *testing.T) {
	// Codex review: each reload left the emptied scope of the last load behind, and AssertNotCalled counted an
	// empty scope as called. Now only a request to a mock fails it, and the report names only that mock.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "file"})

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/code").Reply(OK()))

	require.NoError(t, m.Reload())
	require.NoError(t, m.Reload())

	ft := testmock.NewFakeT()
	require.True(t, m.AssertNotCalled(ft))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	get(t, m, "/f")

	ft = testmock.NewFakeT()
	require.False(t, m.AssertNotCalled(ft))
	ft.AssertNumberOfCalls(t, "Errorf", 1)

	call := ft.Calls[slices.IndexFunc(ft.Calls, func(c mock.Call) bool { return c.Method == "Errorf" })]
	args, _ := call.Arguments.Get(1).([]any)
	report := fmt.Sprintf(call.Arguments.String(0), args...)

	require.Contains(t, report, "1 Mock(s) were called")
	require.Contains(t, report, "[file]")
}

func TestFileLoader_OtherScopesCantDeleteFileMocks(t *testing.T) {
	// Codex review: any scope deleted any ID, so a code scope could remove a file mock and leave the loader holding
	// an ID that a later mock could take, and that the next reload would then delete.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "users"})

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
	m.MustStart()
	t.Cleanup(m.Close)

	code := m.MustMock(Getf("/code").Reply(OK()))
	require.NoError(t, code.Delete(t.Context(), "users"))

	_, body := get(t, m, "/f")
	require.Equal(t, "a.mock.yaml", body)

	require.NoError(t, m.Reload())

	_, body = get(t, m, "/f")
	require.Equal(t, "a.mock.yaml", body)
}

func TestFileLoader_FailedReloadKeepsTheLastMocks(t *testing.T) {
	// A reload removed the mocks of the last load before it built the new ones, so a bad edit left a running server
	// with no file mock until a later load succeeded.
	tests := []struct {
		name  string
		spoil func(t *testing.T, dir string)
		err   string
	}{
		{
			name:  "a file with problems",
			spoil: func(t *testing.T, dir string) { writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{bad: true}) },
			err:   "a.mock.yaml: unknown_key: response.stauts",
		},
		{
			name: "two files with one id",
			spoil: func(t *testing.T, dir string) {
				writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "same"})
				writeMockFile(t, dir, "b.mock.json", mockFileSpec{id: "same"})
			},
			err: `loader: server: store: mock id already in use: "same"`,
		},
		{
			name: "a backup the pattern matches",
			spoil: func(t *testing.T, dir string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.mock.yaml~"), []byte("request: {path: /f}\n"), 0o600))
			},
			err: "a.mock.yaml~: unsupported_format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{})

			m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
			m.MustStart()
			t.Cleanup(m.Close)

			_, body := get(t, m, "/f")
			require.Equal(t, "a.mock.yaml", body)

			tc.spoil(t, dir)
			require.ErrorContains(t, m.Reload(), tc.err)

			// The mocks of the last load still serve, and the assertions still count them.
			_, body = get(t, m, "/f")
			require.Equal(t, "a.mock.yaml", body)
			require.Len(t, m.ListCalled(), 1)
			require.Equal(t, int64(2), m.Hits())

			// The next good reload applies the edit.
			clearDir(t, dir)
			writeMockFile(t, dir, "b.mock.json", mockFileSpec{})
			require.NoError(t, m.Reload())

			_, body = get(t, m, "/f")
			require.Equal(t, "b.mock.json", body)
		})
	}
}

func TestFileLoader_ReloadNeverDropsFileMocks(t *testing.T) {
	// A reload removed the mocks of the last load, then built and added the new ones:
	// a request in between found no mock at all.
	dir := t.TempDir()
	writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "f"})

	m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
	m.MustStart()
	t.Cleanup(m.Close)

	var (
		wg       sync.WaitGroup
		done     atomic.Bool
		requests atomic.Int64
		missed   atomic.Int64
	)

	for range 4 {
		wg.Go(func() {
			for !done.Load() {
				res, err := http.Get(m.URL() + "/f")
				if err != nil {
					continue
				}

				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()

				if res.StatusCode == StatusNoMatch {
					missed.Add(1)
				}

				requests.Add(1)
			}
		})
	}

	for i := range 100 {
		// Each reload replaces the mock with another of the same id.
		writeMockFile(t, dir, "a.mock.yaml", mockFileSpec{id: "f", priority: i % 2})
		require.NoError(t, m.Reload())
	}

	done.Store(true)
	wg.Wait()

	require.Positive(t, requests.Load())
	require.Zero(t, missed.Load(), "requests that found no file mock")
}

// clearDir removes everything dir holds.
func clearDir(t require.TestingT, dir string) {
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
}
