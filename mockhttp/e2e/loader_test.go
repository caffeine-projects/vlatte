package e2e

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestFileLoader_YAMLDocumentsServeEveryMock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`request:
  path: /first
response:
  body: first
---
request:
  path: /second
response:
  body: second
`), 0o600))

	app := mockhttp.MustNew(mockhttp.Setup().Silent().MockFilePatterns(path))
	t.Cleanup(app.Close)
	require.NoError(t, app.Start())

	for _, want := range []string{"first", "second"} {
		res, err := http.Get(app.URL("/" + want))
		require.NoError(t, err)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
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
