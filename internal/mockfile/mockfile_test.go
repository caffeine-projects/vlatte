package mockfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestRead_KeepsTheFileAndItsBodyDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mock.YML")
	data := []byte("id: first\n")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	doc, err := Read(path, nil)

	require.NoError(t, err)
	require.Equal(t, Document{Origin: "file " + path, Source: path, Format: "yml", Dir: dir, Data: data}, doc)
}

func TestRead_ReportsTheFileProblemAndOptionalHint(t *testing.T) {
	dir := t.TempDir()
	directory := filepath.Join(dir, "directory.yaml")
	require.NoError(t, os.Mkdir(directory, 0o700))
	tests := []struct {
		name string
		path string
		hint func(string) string
		code string
		want string
	}{
		{name: "unsupported format", path: filepath.Join(dir, "mock.toml"), code: config.CodeUnsupportedFormat, want: "use a .json, .yaml or .yml file"},
		{name: "missing file", path: filepath.Join(dir, "missing.yaml"), code: config.CodeFileNotFound},
		{
			name: "missing file with help",
			path: filepath.Join(dir, "missing.yaml"),
			hint: func(path string) string { return "find " + path },
			code: config.CodeFileNotFound,
			want: "find " + filepath.Join(dir, "missing.yaml"),
		},
		{name: "unreadable file", path: directory, code: config.CodeReadError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Read(tt.path, tt.hint)
			require.Equal(t, Document{}, doc)
			p := onlyProblem(t, err)
			require.Equal(t, tt.code, p.Code)
			require.Equal(t, "file "+tt.path, p.Origin)
			require.Equal(t, tt.want, p.Hint)
		})
	}
}

func TestLoad_RetainsDocumentOrderAndEveryFileFailure(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	empty := filepath.Join(dir, "empty.yml")
	last := filepath.Join(dir, "last.json")
	missing := filepath.Join(dir, "missing.yaml")
	require.NoError(t, os.WriteFile(first, []byte("id: first\n---\nid: second\n"), 0o600))
	require.NoError(t, os.WriteFile(bad, []byte("id: third\n---\nbad: [\n"), 0o600))
	require.NoError(t, os.WriteFile(empty, []byte("# empty\n"), 0o600))
	require.NoError(t, os.WriteFile(last, []byte(`{"id":"last"}`), 0o600))

	documents := Load([]string{first, bad, empty, last, missing}, nil)

	require.Len(t, documents, 5)
	for i, id := range []string{"first", "second"} {
		doc := documents[i]
		require.NoError(t, doc.Err)
		require.Equal(t, first, doc.Source)
		require.Equal(t, dir, doc.Dir)
		parsed, err := config.Parse(doc.Data, doc.Format)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"id": id}, parsed)
	}
	require.Equal(t, "file "+first+" document 1", documents[0].Origin)
	require.Equal(t, "file "+first+" document 2", documents[1].Origin)
	p := onlyProblem(t, documents[2].Err)
	require.Equal(t, config.CodeParseError, p.Code)
	require.Equal(t, "file "+bad, p.Origin)
	require.ErrorContains(t, documents[2].Err, "document 2")
	require.Equal(t, Document{Origin: "file " + last, Source: last, Format: "json", Dir: dir, Data: []byte(`{"id":"last"}`)}, documents[3])
	p = onlyProblem(t, documents[4].Err)
	require.Equal(t, config.CodeFileNotFound, p.Code)
	require.Equal(t, "file "+missing, p.Origin)
}

func TestSplit_LeavesOtherFormatsForTheMockSchema(t *testing.T) {
	for _, format := range []string{"json", ".JSON", "toml"} {
		t.Run(format, func(t *testing.T) {
			doc := Document{Origin: "bytes", Format: format, Data: []byte(`{} {}`)}
			parts, err := doc.Split()
			require.NoError(t, err)
			require.Equal(t, []Document{doc}, parts)
		})
	}
}

func TestSingle_SkipsEmptyDocumentsAndKeepsPhysicalOrigin(t *testing.T) {
	doc := Document{Origin: "file mock.yml", Source: "mock.yml", Dir: "bodies", Format: ".YML", Data: []byte("---\n---\nid: first\n---\n")}

	single, err := doc.Single("load all")

	require.NoError(t, err)
	require.Equal(t, "file mock.yml document 2", single.Origin)
	require.Equal(t, "mock.yml", single.Source)
	require.Equal(t, "bodies", single.Dir)
	require.Equal(t, "yml", single.Format)
	parsed, err := config.Parse(single.Data, single.Format)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"id": "first"}, parsed)
}

func TestSingle_LeavesEmptyInputForNormalMockValidation(t *testing.T) {
	doc := Document{Origin: "bytes", Format: "yaml", Data: []byte("# empty\n")}
	single, err := doc.Single("load all")
	require.NoError(t, err)
	require.Equal(t, doc, single)
}

func TestSingle_ReportsPluralInputWithTheCallersHint(t *testing.T) {
	doc := Document{Origin: "bytes", Format: "yaml", Data: []byte("id: first\n---\nid: second\n")}
	single, err := doc.Single("use the file loader")
	require.Equal(t, Document{}, single)
	require.Equal(t, config.Problem{
		Code:    config.CodeParseError,
		Origin:  "bytes",
		Message: "expected one mock document, not 2",
		Hint:    "use the file loader",
	}, onlyProblem(t, err))
}

func TestSingle_ReportsMalformedLaterInput(t *testing.T) {
	doc := Document{Origin: "bytes", Format: "yaml", Data: []byte("id: first\n---\nbad: [\n")}
	single, err := doc.Single("load all")
	require.Equal(t, Document{}, single)
	p := onlyProblem(t, err)
	require.Equal(t, config.CodeParseError, p.Code)
	require.Equal(t, "bytes", p.Origin)
	require.ErrorContains(t, err, "document 2")
}

func TestFileBuilder_ReadsTheCurrentFileForEachServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mock.yaml")
	b := File(path, nil, "load all", func(doc Document, server string) (string, error) {
		parsed, err := config.Parse(doc.Data, doc.Format)
		require.NoError(t, err)
		fields, ok := parsed.(map[string]any)
		require.True(t, ok)
		id, ok := fields["id"].(string)
		require.True(t, ok)
		return id + server, nil
	})
	require.NoError(t, os.WriteFile(path, []byte("id: first\n"), 0o600))
	first, err := b.Build("-one")
	require.NoError(t, err)
	require.Equal(t, "first-one", first)
	require.NoError(t, os.WriteFile(path, []byte("id: second\n"), 0o600))
	second, err := b.Build("-two")
	require.NoError(t, err)
	require.Equal(t, "second-two", second)
}

func TestDocumentBuilder_PreservesProtocolErrors(t *testing.T) {
	want := errors.New("protocol refused the mock")
	doc := Document{Origin: "bytes", Format: "yaml", Data: []byte("id: first\n")}
	b := doc.Builder("load all", func(Document, int) (string, error) { return "", want })
	got, err := b.Build(1)
	require.Equal(t, "", got)
	require.ErrorIs(t, err, want)
}

func TestDocumentBuilder_RejectsPluralInputBeforeBuilding(t *testing.T) {
	doc := Document{Origin: "bytes", Format: "yaml", Data: []byte("id: first\n---\nid: second\n")}
	builds := 0
	b := doc.Builder("load all", func(Document, int) (string, error) {
		builds++
		return "unexpected", nil
	})
	got, err := b.Build(1)
	require.Equal(t, "", got)
	require.Equal(t, config.CodeParseError, onlyProblem(t, err).Code)
	require.Equal(t, 0, builds)
}

func TestFileBuilder_ReportsAReadFailureBeforeBuilding(t *testing.T) {
	builds := 0
	b := File(filepath.Join(t.TempDir(), "missing.yaml"), nil, "load all", func(Document, int) (string, error) {
		builds++
		return "unexpected", nil
	})
	got, err := b.Build(1)
	require.Equal(t, "", got)
	require.Equal(t, config.CodeFileNotFound, onlyProblem(t, err).Code)
	require.Equal(t, 0, builds)
}

func TestDecode_LoadsSettingsBeforeCheckingTheirCombination(t *testing.T) {
	var dst string
	schema := config.NewSchema(config.Field[string]{
		Key:  "name",
		Kind: config.String,
		Set:  func(dst *string, v config.Value) error { *dst = v.Str; return nil },
	})
	doc := Document{Origin: "bytes", Format: "json", Data: []byte(`{"name":"first"}`)}
	err := doc.Decode(schema, &dst, func(origin string, _ []config.Problem) []config.Problem {
		require.Equal(t, "first", dst)
		require.Equal(t, "bytes", origin)
		return nil
	})
	require.NoError(t, err)
}

func TestDecode_KeepsSchemaProblemsBeforeCrossFieldProblems(t *testing.T) {
	var dst string
	schema := config.NewSchema(config.Field[string]{Key: "name", Kind: config.String})
	doc := Document{Origin: "bytes", Format: "json", Data: []byte(`{"name":42}`)}
	extra := config.Problem{Code: config.CodeConflict, Origin: "bytes", Key: "response", Message: "response is required"}
	err := doc.Decode(schema, &dst, func(_ string, problems []config.Problem) []config.Problem {
		require.Len(t, problems, 1)
		require.Equal(t, "name", problems[0].Key)
		return []config.Problem{extra}
	})
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok)
	require.Equal(t, "mock", cfgErr.Scope)
	require.Len(t, cfgErr.Problems, 2)
	require.Equal(t, "name", cfgErr.Problems[0].Key)
	require.Equal(t, extra, cfgErr.Problems[1])
}

func TestDecode_DoesNotAddCrossFieldNoiseToParseErrors(t *testing.T) {
	for _, tt := range []struct {
		format string
		origin string
		code   string
	}{
		{format: "json", origin: "bytes:1:2", code: config.CodeParseError},
		{format: "toml", origin: "bytes", code: config.CodeUnsupportedFormat},
	} {
		t.Run(tt.format, func(t *testing.T) {
			var dst string
			schema := config.NewSchema(config.Field[string]{Key: "name", Kind: config.String})
			doc := Document{Origin: "bytes", Format: tt.format, Data: []byte("{")}
			checks := 0
			err := doc.Decode(schema, &dst, func(string, []config.Problem) []config.Problem {
				checks++
				return []config.Problem{{Code: config.CodeConflict, Message: "unexpected"}}
			})
			p := onlyProblem(t, err)
			require.Equal(t, tt.origin, p.Origin)
			require.Equal(t, tt.code, p.Code)
			require.Equal(t, 0, checks)
		})
	}
}

func TestDecode_PreservesAFileFailure(t *testing.T) {
	want := errors.New("file read failed")
	doc := Document{Err: want}
	var dst string
	err := doc.Decode(config.NewSchema(config.Field[string]{Key: "name", Kind: config.String}), &dst, func(string, []config.Problem) []config.Problem {
		t.Fatal("a file failure should not reach validation")
		return nil
	})
	require.ErrorIs(t, err, want)
}

func onlyProblem(t *testing.T, err error) config.Problem {
	t.Helper()
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "mock", cfgErr.Scope)
	require.Len(t, cfgErr.Problems, 1)
	return cfgErr.Problems[0]
}
