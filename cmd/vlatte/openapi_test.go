package main

import (
	"bytes"
	"context"
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
			name: "a null media type",
			spec: write("null-media.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /x:\n    get:\n      responses:\n        \"200\":\n          description: OK\n          content:\n            text/plain: null\n"),
			want: "openapi: invalid spec: media type text/plain: expected an object, not null\n",
		},
		{
			name: "a null webhook",
			spec: write("null-webhook.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths: {}\nwebhooks:\n  /w: null\n"),
			want: "openapi: invalid spec: webhook /w: expected an object, not null\n",
		},
		{
			name: "a null encoding",
			spec: write("null-encoding.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /x:\n    post:\n      requestBody:\n        content:\n          multipart/form-data:\n            encoding:\n              f: null\n      responses:\n        \"204\": {description: OK}\n"),
			want: "openapi: invalid spec: encoding f: expected an object, not null\n",
		},
		{
			name: "a null server of a path",
			spec: write("null-server.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /x:\n    servers: [null]\n    get:\n      responses: {\"200\": {description: OK}}\n"),
			want: "openapi: invalid spec: invalid path /x: invalid servers: value of url must be a non-empty string\n",
		},
		{
			name: "a server of an operation without a URL",
			spec: write("empty-server.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /x:\n    get:\n      servers: [{url: \"\"}]\n      responses: {\"200\": {description: OK}}\n"),
			want: "openapi: invalid spec: invalid path /x: invalid operation GET: invalid servers: value of url must be a non-empty string\n",
		},
		{
			name: "a null variable of a server of an operation",
			spec: write("null-variable.yaml", "openapi: 3.1.0\ninfo: {title: X, version: \"1\"}\npaths:\n  /x:\n    get:\n      servers: [{url: \"/{v}\", variables: {v: null}}]\n      responses: {\"200\": {description: OK}}\n"),
			want: "openapi: invalid spec: invalid path /x: invalid operation GET: invalid servers: field default is required in {}\n",
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
		{
			name: "a URL that doesn't parse",
			spec: "http://%zz",
			want: "openapi: parsing the spec's URL: parse \"http://%zz\": invalid URL escape \"%zz\"\n",
		},
		{
			name: "no file",
			spec: filepath.Join(dir, "missing.yaml"),
			want: "openapi: opening the file: open " + filepath.Join(dir, "missing.yaml") + ": no such file or directory\n",
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

func TestOpenAPI_WarnsAndListsWhatItNoLongerWrites(t *testing.T) {
	// A rerun after the spec dropped an operation leaves its file, which still loads: the user hears of it.
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.yaml")
	out := filepath.Join(dir, "mocks")
	write := func(paths string) {
		require.NoError(t, os.WriteFile(spec, []byte(`openapi: 3.1.0
info: {title: X, version: "1"}
servers: [{url: "/v1"}, {url: "/v2"}]
paths:
`+paths), 0o600))
	}

	write("  /a: {get: {responses: {\"204\": {description: OK}}}}\n  /b: {get: {responses: {\"204\": {description: OK}}}}\n")

	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, &stderr))
	require.Regexp(t, `^\S+ WRN the servers have different paths: the mocks answer under the first one's, "/v1"\n$`, stderr.String())

	write("  /a: {get: {responses: {\"204\": {description: OK}}}}\n")
	stdout.Reset()
	require.Equal(t, 0, run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, io.Discard))
	require.Contains(t, stdout.String(), "These files were generated from this spec, but no longer are, and still load: v1-b-get-204.mock.yaml\n")

	// A file of the folder that the command didn't write stays as it is.
	require.NoError(t, os.WriteFile(filepath.Join(out, "v1-a-get-204.mock.yaml"), []byte("id: mine\n"), 0o600))
	stderr.Reset()
	require.Equal(t, 1, run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, io.Discard, &stderr))
	require.Contains(t, stderr.String(), "\nopenapi: a file to write is not one generated from this spec: v1-a-get-204.mock.yaml: move them, or write to another folder\n")
}

func TestOpenAPI_LogsWhatItCannotHonorOnStandardError(t *testing.T) {
	// The log names the mock to fix or enable, and keeps standard output for the result, which scripts read.
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.yaml")
	out := filepath.Join(dir, "mocks")
	require.NoError(t, os.WriteFile(spec, []byte(`openapi: 3.1.0
info: {title: X, version: "1"}
paths:
  /report: {get: {responses: {"200": {description: OK, content: {text/csv: {example: [a, b]}}}}}}
`), 0o600))

	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run(t.Context(), []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, &stderr))
	require.Regexp(t, `^\S+ WRN the body is data, not text: written disabled: give a string example `+
		`media_type=text/csv method=GET mock=report-get-200-text-csv route=/report status=200\n$`, stderr.String())
	require.Equal(t, "Wrote 2 mock files to "+out+".\nServe them with: vlatte --mock-files '"+filepath.Join(out, "*.mock.yaml")+"'\n", stdout.String())
}

func TestOpenAPI_StopsWhenInterrupted(t *testing.T) {
	// A large spec takes minutes: Ctrl+C must stop the command, and leave the folder as it was.
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.yaml")
	out := filepath.Join(dir, "mocks")
	require.NoError(t, os.WriteFile(spec, []byte(`openapi: 3.1.0
info: {title: X, version: "1"}
paths:
  /a: {get: {responses: {"204": {description: Done}}}}
`), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run(ctx, []string{"openapi", "--in", spec, "--out", out}, nil, &stdout, &stderr))
	require.Equal(t, "openapi: generating the mocks: context canceled\n", stderr.String())
	require.Empty(t, stdout.String())
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
