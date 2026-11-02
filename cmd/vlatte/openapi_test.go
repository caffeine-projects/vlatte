package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// _update rewrites the expected output of the openapi command: go test ./cmd/vlatte -run TestOpenAPI -update.
var _update = flag.Bool("update", false, "rewrite the expected mock files of the openapi command")

func TestOpenAPI_WritesTheMocksOfThePetstore(t *testing.T) {
	// The repository's petstore spec covers every kind of operation the generator handles: a change to what it
	// writes shows here, file by file, as a reviewer would see it in a regenerated folder.
	//
	// The tests run in a folder of their own: the package's is where this file is.
	_, file, _, _ := runtime.Caller(0)
	pkg := filepath.Dir(file)
	spec := filepath.Join(pkg, "..", "..", "examples", "_specs", "petstore.openapi.yaml")
	golden := filepath.Join(pkg, "testdata", "openapi", "petstore")
	out := filepath.Join(t.TempDir(), "mocks")

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	require.Empty(t, stderr.String())
	require.Equal(t, "Wrote 45 mock files to "+out+".\nServe them with: vlatte --mock-files '"+filepath.Join(out, "*.mock.yaml")+"'\n", stdout.String())

	if *_update {
		require.NoError(t, os.RemoveAll(golden))
		require.NoError(t, os.CopyFS(golden, os.DirFS(out)))
	}

	want, err := os.ReadDir(golden)
	require.NoError(t, err)

	got, err := os.ReadDir(out)
	require.NoError(t, err)
	require.Equal(t, names(want), names(got))

	for _, e := range want {
		wantData, err := os.ReadFile(filepath.Join(golden, e.Name()))
		require.NoError(t, err)

		gotData, err := os.ReadFile(filepath.Join(out, e.Name()))
		require.NoError(t, err)
		require.Equal(t, string(wantData), string(gotData), e.Name())
	}

	// A rerun writes the same files, and lists none as stale.
	stdout.Reset()
	require.Equal(t, 0, run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, &stderr))
	require.Contains(t, stdout.String(), "Wrote 45 mock files")
	require.NotContains(t, stdout.String(), "no longer")
}

func TestOpenAPI_FailsOnASpecItCannotUse(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		return path
	}

	tests := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "an invalid spec",
			spec: write("invalid.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /a:\n    get:\n      parameters: [{name: a, in: querya}]\n"),
			want: "openapi: invalid spec: invalid paths: invalid path /a: invalid operation GET: parameter can't have 'in' value \"querya\"\n",
		},
		{
			name: "Swagger",
			spec: write("swagger.yaml", "swagger: \"2.0\"\ninfo: {title: X, version: \"1\"}\npaths: {}\n"),
			want: "openapi: version 2.0 is not supported: expected OpenAPI 3.0, 3.1 or 3.2\n",
		},
		{
			name: "no spec",
			spec: write("other.yaml", "name: x\n"),
			want: "openapi: not an OpenAPI or Swagger document: expected an openapi or swagger field with its version, such as openapi: 3.1.0\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "mocks")

			var stderr bytes.Buffer
			require.Equal(t, 1, run(t.Context(), []string{"openapi", "--in", tt.spec, "--out", out}, nil, io.Discard, &stderr))
			require.Equal(t, tt.want, stderr.String())
			require.NoDirExists(t, out)
		})
	}
}

func TestOpenAPI_ASpecOverHTTPReadsNoLocalFile(t *testing.T) {
	// Whoever serves a spec decides what it refers to: a remote one must not copy a local file into the mocks.
	local := filepath.Join(t.TempDir(), "secret.yaml")
	require.NoError(t, os.WriteFile(local, []byte("{type: string, example: secret}\n"), 0o600))

	ref := (&url.URL{Scheme: "file", Path: filepath.ToSlash(local)}).String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `openapi: 3.1.0
info: {title: X, version: "1"}
paths:
  /a:
    get:
      responses:
        "200":
          description: OK
          content:
            text/plain:
              schema: {$ref: "`+ref+`"}
`)
	}))
	t.Cleanup(srv.Close)

	out := filepath.Join(t.TempDir(), "mocks")

	var stderr bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"openapi", "--in", srv.URL + "/spec.yaml", "--out", out}, nil, io.Discard, &stderr))
	require.Contains(t, stderr.String(), fmt.Sprintf("openapi: %s/spec.yaml refers to %s: a spec over http(s) refers to http(s) URLs only", srv.URL, ref))
	require.NoDirExists(t, out)
}

func TestOpenAPI_TakesAnInAndAnOut(t *testing.T) {
	var stderr bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"openapi", "--in", "spec.yaml"}, nil, io.Discard, &stderr))
	require.Equal(t, "required flag(s) \"out\" not set\n"+_helpHint+"\n", stderr.String())
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}

	return out
}
