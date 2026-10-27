package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileLoader_Load(t *testing.T) {
	app := MustNew(Setup().MockFilePatterns("testdata/loader/load/*mock.json", "testdata/loader/load/*.json"))
	loader := &fileLoader{}

	err := loader.Load(app)
	require.NoError(t, err)

	all, err := app.storage.PickAll(context.Background())
	require.NoError(t, err)

	require.NoError(t, err)
	require.Equal(t, 2, len(all))
}

func TestFileLoader_LoadWithError(t *testing.T) {
	// More than one invalid file: Load must still return the error instead of hanging.
	app := MustNew(Setup().MockFilePatterns("testdata/loader/invalid/*.json"))
	loader := &fileLoader{}

	done := make(chan error, 1)
	go func() { done <- loader.Load(app) }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Load did not return")
	}
}

func TestFileLoader(t *testing.T) {
	app := MustNew(Setup().MockFilePatterns("testdata/loader/*.mock.*"))
	app.MustStart()

	defer app.Close()

	httpClient := &http.Client{}
	res, err := httpClient.Get(app.URL() + "/test?term=test&filter=all+none&page=10")
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	res.Body.Close()

	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "hi", string(b))

	res, err = httpClient.Get(app.URL() + "/test/002")
	require.NoError(t, err)

	b, err = io.ReadAll(res.Body)
	res.Body.Close()

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "test ok\n", string(b))

	res, err = httpClient.Get(app.URL() + "/test003?term=test&filter=none")
	require.NoError(t, err)

	body := make([]map[string]any, 0)
	err = json.UnmarshalRead(res.Body, &body)
	res.Body.Close()

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Len(t, body, 0)
}

func TestFileLoader_LoadsEveryMockOfAMultiDocumentYAMLFile(t *testing.T) {
	// A YAML file can hold several mocks, one per document: none may be dropped.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "all_mock.yaml"), `---
request: {method: GET, path: /a}
response: {status: 201}
---
# the second mock
request: {method: GET, path: /b}
response: {status: 202}
...
---
request: {method: GET, path: /c}
response: {status: 203}
---
`)

	app := MustNew(Setup().RootDir(dir).MockFilePatterns("*_mock.yaml"))
	app.MustStart()

	defer app.Close()

	for path, status := range map[string]int{"/a": http.StatusCreated, "/b": http.StatusAccepted, "/c": http.StatusNonAuthoritativeInfo} {
		res, err := http.Get(app.URL() + path)
		require.NoError(t, err)

		res.Body.Close()
		require.Equal(t, status, res.StatusCode, path)
	}
}

func TestFileLoader_AProblemNamesItsDocument(t *testing.T) {
	// In a file of several mocks, a problem says which one has it, and a syntax error keeps the line of the file.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "all_mock.yaml"), `request: {method: GET, path: /a}
response: {status: 200}
---
request: {method: GET, path: /b}
response: {stauts: 200}
---
request: [
`)

	app := MustNew(Setup().RootDir(dir).MockFilePatterns("*_mock.yaml"))

	err := (&fileLoader{}).Load(app)
	require.ErrorContains(t, err, "all_mock.yaml, mock 2: unknown_key: response.stauts")
	require.ErrorContains(t, err, "all_mock.yaml, mock 3: parse_error: yaml: line 7:")
}
