package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// _openapiTimeout bounds each fetch of a spec, or of a file it refers to, over HTTP.
const _openapiTimeout = 30 * time.Second

// openapiCmd writes mock files from an OpenAPI spec. It sets code to 1 when it fails.
func openapiCmd(code *int) *cobra.Command {
	var in, out string

	cmd := &cobra.Command{
		Use:   "openapi --in <spec> --out <dir>",
		Short: "write mock files from an OpenAPI spec",
		Long: `Writes a mock file for each response of each operation of an OpenAPI 3.0, 3.1 or 3.2 spec, in JSON or YAML,
from a file or an http(s) URL. Bodies are the spec's examples, or fake values that fit their schemas, the same on
every run. Each media type of a response gets a mock, which answers the client whose Accept prefers it, and each
operation a 406 for a client that takes none. Responses nothing in a request predicts, such as 503, are written
disabled: the admin API enables them. Mocks written by hand win over them.
Each file names the title of its spec. A rerun overwrites the files written from a spec of that title, lists those
it no longer writes, and never touches other files: write specs that share a title to different folders.
A spec over http(s) refers to http(s) URLs only.`,
		Example: "  vlatte openapi --in petstore.yaml --out testdata/vlatte/_mocks\n" +
			"  vlatte openapi --in https://example.com/openapi.json --out mocks\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			*code = writeOpenAPIMocks(cmd.Context(), in, out, cmd.OutOrStdout(), cmd.ErrOrStderr())
			return nil
		},
	}

	cmd.Flags().StringVar(&in, "in", "", "the spec: a file or an http(s) URL, in JSON or YAML")
	cmd.Flags().StringVar(&out, "out", "", "the folder to write the mock files to, created if missing")
	_ = cmd.MarkFlagRequired("in")
	_ = cmd.MarkFlagRequired("out")

	return cmd
}

// writeOpenAPIMocks writes the mock files of the spec in to the folder out, and returns the exit code: 0, or 1 when
// it fails.
func writeOpenAPIMocks(ctx context.Context, in, out string, stdout, stderr io.Writer) int {
	doc, err := loadOpenAPI(ctx, in)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	gen, err := openapi.Generate(doc)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	for _, w := range append(doc.Warnings, gen.Warnings...) {
		_, _ = fmt.Fprintln(stderr, "warning:", w)
	}

	report, err := openapi.Write(out, doc.Title, gen.Files)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "Wrote %d mock files to %s.\n", len(report.Written), out)

	if len(report.Stale) > 0 {
		_, _ = fmt.Fprintf(stdout, "These files were generated from this spec, but no longer are, and still load: %s\n",
			strings.Join(report.Stale, ", "))
	}

	if filepath.Clean(out) != filepath.Join(mockhttp.DefaultWorkDir, "_mocks") {
		_, _ = fmt.Fprintf(stdout, "Serve them with: vlatte --mock-files '%s'\n", filepath.Join(out, "*.mock.yaml"))
	}

	return 0
}

// loadOpenAPI reads the spec in, a file or an http(s) URL, with the package of its version.
func loadOpenAPI(ctx context.Context, in string) (*openapi.Document, error) {
	loc, err := openapi.Location(in)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: _openapiTimeout}
	read := func(u *url.URL) ([]byte, error) {
		// Whoever serves a spec decides what it refers to: it must not copy a local file into the mocks.
		if remote(loc) && !remote(u) {
			return nil, fmt.Errorf("openapi: %s refers to %s: a spec over http(s) refers to http(s) URLs only", loc, u)
		}

		return openapi.Read(ctx, client, u)
	}

	data, err := read(loc)
	if err != nil {
		return nil, err
	}

	version, err := openapi.Detect(data)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(version, "3.") {
		return nil, fmt.Errorf("openapi: version %s is not supported: expected OpenAPI 3.0, 3.1 or 3.2", version)
	}

	return openapiv3.Load(ctx, data, loc, read)
}

// remote reports whether u is an http(s) URL.
func remote(u *url.URL) bool {
	return u.Scheme == "http" || u.Scheme == "https"
}
