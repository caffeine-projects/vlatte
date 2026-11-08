package openapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestLocation_ResolvesAFileToItsAbsolutePath(t *testing.T) {
	// Relative references resolve against the spec's own location, wherever the command runs.
	u, err := openapi.Location("specs/pets.yaml")
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, &url.URL{Path: filepath.ToSlash(filepath.Join(wd, "specs", "pets.yaml"))}, u)

	u, err = openapi.Location("https://example.com/specs/pets.yaml")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/specs/pets.yaml", u.String())

	_, err = openapi.Location("http://%zz")
	require.EqualError(t, err, `openapi: parsing the spec's URL: parse "http://%zz": invalid URL escape "%zz"`)
}

func TestRead_ReadsAFileOrAURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pets.yaml")
	require.NoError(t, os.WriteFile(path, []byte("openapi: 3.1.0\n"), 0o600))

	u, err := openapi.Location(path)
	require.NoError(t, err)

	data, err := openapi.Read(t.Context(), http.DefaultClient, u)
	require.NoError(t, err)
	require.Equal(t, "openapi: 3.1.0\n", string(data))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pets.yaml":
			_, _ = w.Write([]byte("openapi: 3.0.3\n"))
		case "/big.yaml":
			_, _ = w.Write([]byte(strings.Repeat("a", openapi.MaxSpecSize+1)))
		case "/cut.yaml":
			// A body shorter than its length: the connection ends before it does.
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("openapi"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	u, err = url.Parse(srv.URL + "/pets.yaml")
	require.NoError(t, err)

	data, err = openapi.Read(t.Context(), srv.Client(), u)
	require.NoError(t, err)
	require.Equal(t, "openapi: 3.0.3\n", string(data))

	u, err = url.Parse(srv.URL + "/missing.yaml")
	require.NoError(t, err)

	_, err = openapi.Read(t.Context(), srv.Client(), u)
	require.EqualError(t, err, "openapi: GET "+u.String()+": 404 Not Found")

	u, err = url.Parse(srv.URL + "/big.yaml")
	require.NoError(t, err)

	_, err = openapi.Read(t.Context(), srv.Client(), u)
	require.ErrorIs(t, err, openapi.ErrTooLarge)

	u, err = url.Parse(srv.URL + "/cut.yaml")
	require.NoError(t, err)

	_, err = openapi.Read(t.Context(), srv.Client(), u)
	require.EqualError(t, err, "openapi: reading "+u.String()+": unexpected EOF")

	_, err = openapi.Read(t.Context(), srv.Client(), &url.URL{Scheme: "ftp", Host: "example.com", Path: "/a"})
	require.EqualError(t, err, `openapi: expected a file or an http(s) URL, not "ftp://example.com/a"`)

	_, err = openapi.Read(t.Context(), srv.Client(), &url.URL{Path: filepath.ToSlash(filepath.Join(dir, "missing.yaml"))})
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "openapi: opening the file: ")

	srv.Close()

	_, err = openapi.Read(t.Context(), srv.Client(), u)
	require.ErrorContains(t, err, "openapi: fetching: Get \""+u.String()+"\": ")
}

func TestDetect_ReadsTheVersionAsWritten(t *testing.T) {
	// The version picks the package that reads the spec: swagger: 2.0 is a number to YAML, and must not read as 2.
	tests := []struct {
		name string
		spec string
		want string
	}{
		{name: "OpenAPI 3.1 in YAML", spec: "openapi: 3.1.0\ninfo: {}\n", want: "3.1.0"},
		{name: "OpenAPI 3.0 in JSON", spec: `{"openapi": "3.0.3", "paths": {}}`, want: "3.0.3"},
		{name: "Swagger as a number", spec: "swagger: 2.0\n", want: "2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openapi.Detect([]byte(tt.spec))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := openapi.Detect([]byte("{"))
	require.ErrorIs(t, err, openapi.ErrNotOpenAPI)
	require.ErrorContains(t, err, "openapi: not an OpenAPI or Swagger document: yaml: ")

	_, err = openapi.Detect([]byte("name: not a spec\n"))
	require.ErrorIs(t, err, openapi.ErrNotOpenAPI)
	require.EqualError(t, err, "openapi: not an OpenAPI or Swagger document: expected an openapi or swagger field with its version, such as openapi: 3.1.0")
}
