package e2e

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestFileLoader_DocumentedYAMLDocumentsServeEveryMock(t *testing.T) {
	doc, err := os.ReadFile("../../docs/http/mock-files.md")
	require.NoError(t, err)
	_, section, ok := strings.Cut(string(doc), "## Multiple mocks in a YAML file")
	require.True(t, ok)
	_, example, ok := strings.Cut(section, "```yaml\n")
	require.True(t, ok)
	example, _, ok = strings.Cut(example, "\n```")
	require.True(t, ok)
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, []byte(example), 0o600))

	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	for i, want := range []string{"first", "second"} {
		res, err := http.Get(app.URL("/" + want))
		require.NoError(t, err)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusCreated+i, res.StatusCode)
		require.Equal(t, want, string(body))
	}
}

func TestFileLoader_YAMLDocumentsKeepFileAndDocumentOrder(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(`repeat: 1
request: {path: /order}
response: {body: first}
---
repeat: 1
request: {path: /order}
response: {body: second}
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(`request: {path: /order}
response: {body: third}
`), 0o600))

	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.yaml")))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	for _, want := range []string{"first", "second", "third"} {
		status, body := getLoadedMock(t, app, "/order")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, want, body)
	}
}

func TestFileLoader_YAMLDocumentsSkipEmptyDocuments(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "empty file", want: mockhttp.StatusNoMatch},
		{name: "comments and separators", data: "# empty\n---\n# empty too\n---\n", want: mockhttp.StatusNoMatch},
		{
			name: "around a mock",
			data: "---\n# empty\n---\nrequest: {path: /empty}\nresponse: {status: 204}\n---\n---\n",
			want: http.StatusNoContent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mocks.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.data), 0o600))
			app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
			t.Cleanup(app.Close)
			require.NoError(t, app.Start())

			status, _ := getLoadedMock(t, app, "/empty")
			require.Equal(t, tt.want, status)
		})
	}
}

func TestFileLoader_YAMLDocumentsResolveRelativeBodyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mocks.yaml")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "body.txt"), []byte("from file"), 0o600))
	require.NoError(t, os.WriteFile(path, []byte(`request: {path: /first}
response: {body_file: body.txt}
---
request: {path: /second}
response: {body_file: body.txt}
`), 0o600))
	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	for _, target := range []string{"/first", "/second"} {
		status, body := getLoadedMock(t, app, target)
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "from file", body)
	}
}

func TestFileLoader_InvalidLaterYAMLDocumentKeepsLoadedMocks(t *testing.T) {
	tests := []struct {
		name string
		data string
		code string
		key  string
	}{
		{name: "malformed", data: "response: [", code: config.CodeParseError},
		{name: "unknown key", data: "request: {path: /bad}\nresponse: {stauts: 200}\n", code: config.CodeUnknownKey, key: "response.stauts"},
		{name: "null", data: "null\n", code: config.CodeMissingValue, key: "request"},
		{name: "scalar", data: "invalid\n", code: config.CodeParseError},
		{name: "list", data: "[]\n", code: config.CodeParseError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mocks.yaml")
			require.NoError(t, os.WriteFile(path, []byte("request: {path: /first}\nresponse: {body: old}\n---\nrequest: {path: /second}\nresponse: {body: old too}\n"), 0o600))
			app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
			t.Cleanup(app.Close)
			require.NoError(t, app.Start())

			require.NoError(t, os.WriteFile(path, []byte("request: {path: /first}\nresponse: {body: new}\n---\n"+tt.data), 0o600))
			err := app.Reload()
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Len(t, cfgErr.Problems, 1)
			require.Equal(t, tt.code, cfgErr.Problems[0].Code)
			require.Equal(t, tt.key, cfgErr.Problems[0].Key)
			require.ErrorContains(t, err, "document 2")

			status, body := getLoadedMock(t, app, "/first")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, "old", body)
			status, body = getLoadedMock(t, app, "/second")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, "old too", body)
		})
	}
}

func TestFileLoader_DuplicateYAMLDocumentIDsKeepLoadedMocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, []byte("id: old\nrequest: {path: /old}\nresponse: {body: old}\n"), 0o600))
	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())
	require.NoError(t, os.WriteFile(path, []byte(`id: duplicate
request: {path: /new}
response: {body: new}
---
id: duplicate
request: {path: /second}
response: {body: second}
`), 0o600))

	require.ErrorIs(t, app.Reload(), vlatte.ErrDuplicateID)
	status, body := getLoadedMock(t, app, "/old")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "old", body)
	status, _ = getLoadedMock(t, app, "/new")
	require.Equal(t, mockhttp.StatusNoMatch, status)
}

func TestFileLoader_YAMLDocumentErrorsReportEveryInvalidFile(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.yaml")
	require.NoError(t, os.WriteFile(old, []byte(`request: {path: /old}
response: {body: old}
`), 0o600))
	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(filepath.Join(dir, "*.yaml")))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	invalid := filepath.Join(dir, "invalid.yaml")
	malformed := filepath.Join(dir, "malformed.yaml")
	require.NoError(t, os.WriteFile(invalid, []byte(`request: {path: /new}
response: {body: new}
---
# An empty document keeps its place in diagnostics.
---
request: {path: /invalid}
response: {stauts: 200, bodi: invalid}
`), 0o600))
	require.NoError(t, os.WriteFile(malformed, []byte(`request: {path: /new}
response: {body: new}
---
response: [
`), 0o600))

	err := app.Reload()
	require.ErrorContains(t, err, "file "+invalid+" document 3")
	require.ErrorContains(t, err, "response.stauts")
	require.ErrorContains(t, err, "response.bodi")
	require.ErrorContains(t, err, "file "+malformed)
	require.ErrorContains(t, err, "yaml: document 2:")
	status, body := getLoadedMock(t, app, "/old")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "old", body)
	status, _ = getLoadedMock(t, app, "/new")
	require.Equal(t, mockhttp.StatusNoMatch, status)
}

func TestFileLoader_RemovingYAMLDocumentsReplacesLoadedMocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, []byte("request: {path: /first}\nresponse: {body: first}\n---\nrequest: {path: /second}\nresponse: {body: second}\n"), 0o600))
	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	require.NoError(t, os.WriteFile(path, []byte("request: {path: /first}\nresponse: {body: updated}\n"), 0o600))
	require.NoError(t, app.Reload())
	status, body := getLoadedMock(t, app, "/first")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "updated", body)
	status, _ = getLoadedMock(t, app, "/second")
	require.Equal(t, mockhttp.StatusNoMatch, status)

	require.NoError(t, os.WriteFile(path, []byte("# no mocks\n---\n"), 0o600))
	require.NoError(t, app.Reload())
	status, _ = getLoadedMock(t, app, "/first")
	require.Equal(t, mockhttp.StatusNoMatch, status)
}

func getLoadedMock(t *testing.T, app *mockhttp.Server, path string) (int, string) {
	t.Helper()
	res, err := http.Get(app.URL(path))
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	return res.StatusCode, string(body)
}

func TestDiscovery_FileAndGoMocksSurviveReload(t *testing.T) {
	clearHTTPDiscoveryEnv(t)
	t.Chdir(t.TempDir())
	app := newHTTPDiscoveryServer(t, mockhttp.Setup())
	app.MustMock(mockhttp.Getf("/go").Reply(mockhttp.OK().PlainText("from Go")))
	require.NoError(t, app.Start())
	require.NoError(t, app.Reload())
	status, _ := getLoadedMock(t, app, "/fixture")
	require.Equal(t, mockhttp.StatusNoMatch, status)

	writeHTTPDiscoveryFile(t, "testdata/vlatte/_mocks_grpc/neighbor.mock.yaml", "request: [")
	path := "testdata/vlatte/_mocks/discovered.mock.yaml"
	writeHTTPDiscoveryFile(t, path, "request: {path: /fixture}\nresponse: {body: initial}\n")
	restarted := newHTTPDiscoveryServer(t, mockhttp.Setup())
	restarted.MustMock(mockhttp.Getf("/go").Reply(mockhttp.OK().PlainText("from Go")))
	require.NoError(t, restarted.Start())
	requireHTTPDiscoveryReply(t, restarted, "/fixture", "initial")
	requireHTTPDiscoveryReply(t, restarted, "/go", "from Go")

	writeHTTPDiscoveryFile(t, path, "request: {path: /fixture}\nresponse: {body: updated}\n")
	require.NoError(t, restarted.Reload())
	requireHTTPDiscoveryReply(t, restarted, "/fixture", "updated")
	requireHTTPDiscoveryReply(t, restarted, "/go", "from Go")

	writeHTTPDiscoveryFile(t, path, "request: [")
	requireHTTPDiscoveryError(t, restarted.Reload(), config.CodeParseError, "discovered.mock.yaml")
	requireHTTPDiscoveryReply(t, restarted, "/fixture", "updated")
	requireHTTPDiscoveryReply(t, restarted, "/go", "from Go")
}

func TestSkipDefaults_LoadsOnlySelectedFixtures(t *testing.T) {
	clearHTTPDiscoveryEnv(t)

	t.Run("malformed default config", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeHTTPDiscoveryFile(t, ".vlatterc.yaml", "request: [")
		_, err := mockhttp.New(mockhttp.Setup())
		requireHTTPDiscoveryError(t, err, config.CodeParseError, ".vlatterc.yaml")
		requireOnlySelectedHTTPFixtures(t)
	})

	t.Run("hostile environment redirect", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("VLATTE_CONFIG", "absent.yaml")
		_, err := mockhttp.New(mockhttp.Setup())
		requireHTTPDiscoveryError(t, err, config.CodeFileNotFound, "absent.yaml")
		requireOnlySelectedHTTPFixtures(t)
	})

	t.Run("malformed conventional fixture", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeHTTPDiscoveryFile(t, "testdata/vlatte/_mocks/broken.mock.yaml", "request: [")
		ordinary := newHTTPDiscoveryServer(t, mockhttp.Setup())
		requireHTTPDiscoveryError(t, ordinary.Start(), config.CodeParseError, "broken.mock.yaml")
		requireOnlySelectedHTTPFixtures(t)
	})
}

func requireOnlySelectedHTTPFixtures(t *testing.T) {
	t.Helper()
	writeHTTPDiscoveryFile(t, "selected.mock.yaml", "request: {path: /selected}\nresponse: {body: selected fixture}\n")
	skip := newHTTPDiscoveryServer(t, mockhttp.Setup().SkipDefaults().MockFilePatterns("selected.mock.yaml"))
	skip.MustMock(mockhttp.Getf("/go").Reply(mockhttp.OK().PlainText("explicit Go mock")))
	require.NoError(t, skip.Start())
	requireHTTPDiscoveryReply(t, skip, "/selected", "selected fixture")
	requireHTTPDiscoveryReply(t, skip, "/go", "explicit Go mock")
}

type appendHTTPMockPattern string

func (pattern appendHTTPMockPattern) Apply(conf *mockhttp.Config) error {
	conf.MockFileSearchPatterns = append(conf.MockFileSearchPatterns, string(pattern))
	return nil
}

func TestDiscovery_CustomOptionCanAppendToHTTPDefaults(t *testing.T) {
	clearHTTPDiscoveryEnv(t)
	t.Chdir(t.TempDir())
	writeHTTPDiscoveryFile(t, "testdata/vlatte/_mocks/conventional.mock.yaml", "request: {path: /conventional}\nresponse: {body: conventional}\n")
	writeHTTPDiscoveryFile(t, "added.mock.yaml", "request: {path: /added}\nresponse: {body: added}\n")
	writeHTTPDiscoveryFile(t, "source.mock.yaml", "request: {path: /source}\nresponse: {body: source}\n")
	added := appendHTTPMockPattern("added.mock.yaml")
	setup := mockhttp.Setup()

	ordinary := newHTTPDiscoveryServer(t, setup, added)
	require.NoError(t, ordinary.Start())
	requireHTTPDiscoveryReply(t, ordinary, "/conventional", "conventional")
	requireHTTPDiscoveryReply(t, ordinary, "/added", "added")

	t.Setenv("VLATTE_MOCK_FILES", "source.mock.yaml")
	fromSource := newHTTPDiscoveryServer(t, setup, added)
	require.NoError(t, fromSource.Start())
	requireHTTPDiscoveryReply(t, fromSource, "/source", "source")
	status, _ := getLoadedMock(t, fromSource, "/added")
	require.Equal(t, mockhttp.StatusNoMatch, status)

	cleared := newHTTPDiscoveryServer(t, setup, added, mockhttp.Setup().SkipDefaults())
	require.NoError(t, cleared.Start())
	status, _ = getLoadedMock(t, cleared, "/conventional")
	require.Equal(t, mockhttp.StatusNoMatch, status)
	status, _ = getLoadedMock(t, cleared, "/added")
	require.Equal(t, mockhttp.StatusNoMatch, status)

	afterSkip := newHTTPDiscoveryServer(t, mockhttp.Setup().SkipDefaults(), added)
	require.NoError(t, afterSkip.Start())
	requireHTTPDiscoveryReply(t, afterSkip, "/added", "added")
	status, _ = getLoadedMock(t, afterSkip, "/conventional")
	require.Equal(t, mockhttp.StatusNoMatch, status)
}

func newHTTPDiscoveryServer(t *testing.T, options ...mockhttp.Option) *mockhttp.Server {
	t.Helper()
	app := mockhttp.MustNew(append(options, mockhttp.Setup().Silent().Addr("127.0.0.1:0"))...)
	t.Cleanup(app.Close)
	return app
}

func writeHTTPDiscoveryFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func requireHTTPDiscoveryError(t *testing.T, err error, code, file string) {
	t.Helper()
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Len(t, cfgErr.Problems, 1)
	require.Equal(t, code, cfgErr.Problems[0].Code)
	require.ErrorContains(t, err, file)
}

func requireHTTPDiscoveryReply(t *testing.T, app *mockhttp.Server, path, want string) {
	t.Helper()
	status, body := getLoadedMock(t, app, path)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, want, body)
}

func clearHTTPDiscoveryEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"VLATTE_ADDR", "VLATTE_CONFIG", "VLATTE_MOCK_FILES", "VLATTE_NAME", "VLATTE_ROOT_DIR", "VLATTE_WORKDIR"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}
