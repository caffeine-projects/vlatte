package mockhttp

import (
	"context"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
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
	// matching in priority then name order; a failed one leaves no file mock and names every bad file;
	// mocks added in code are never touched.
	names := []string{"a.mock.yaml", "b.mock.json", "c.mock.yaml", "d.mock.yml"}
	ids := []string{"", "", "x", "y"}

	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp("", "loader")
		require.NoError(t, err)

		defer os.RemoveAll(dir)

		m := MustNew(Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.mock.*")))
		m.MustStart()

		defer m.Close()

		m.MustMock(Getf("/code").Reply(OK().PlainText("code")))

		files := make(map[string]mockFileSpec)

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

				all, _ := m.storage.PickAll(context.Background())

				switch {
				case len(bad) > 0:
					for _, name := range bad {
						require.ErrorContains(t, err, "file "+filepath.Join(dir, name)+":")
					}

					require.Len(t, all, 1, "only the code mock")
				case duplicate:
					require.ErrorIs(t, err, vlatte.ErrDuplicateID)
					require.Len(t, all, 1, "only the code mock")
				default:
					require.NoError(t, err)
					require.Len(t, all, 1+len(files))

					var origins []string
					for _, mock := range all {
						if mock.Origin() != "" {
							origins = append(origins, filepath.Base(mock.Origin()))
						}
					}

					require.ElementsMatch(t, slices.Collect(maps.Keys(files)), origins)

					status, body := get(t, m, "/f")
					if len(files) == 0 {
						require.Equal(t, StatusNoMatch, status)
						break
					}

					// The lowest priority wins; then the first name.
					want := slices.MinFunc(slices.Sorted(maps.Keys(files)), func(a, b string) int { return files[a].priority - files[b].priority })
					require.Equal(t, want, body)
				}

				_, body := get(t, m, "/code")
				require.Equal(t, "code", body)
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
