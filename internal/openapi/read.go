package openapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxSpecSize bounds a spec, and each file it refers to, in bytes.
const MaxSpecSize = 32 << 20

// Location returns the URL of in, a file path or an http(s) URL, which the relative references of the spec it names
// resolve against.
func Location(in string) (*url.URL, error) {
	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		u, err := url.Parse(in)
		if err != nil {
			return nil, fmt.Errorf("openapi: parsing the spec's URL: %w", err)
		}

		return u, nil
	}

	// Abs fails only without a working directory: reading the spec fails then.
	abs, _ := filepath.Abs(in)

	return &url.URL{Path: filepath.ToSlash(abs)}, nil
}

// Read returns the content of the file or the http(s) URL u, which client gets, up to [MaxSpecSize] bytes. A URL
// that doesn't answer 200 fails, as does one past [MaxSpecSize], with [ErrTooLarge].
func Read(ctx context.Context, client *http.Client, u *url.URL) ([]byte, error) {
	var r io.ReadCloser

	switch u.Scheme {
	case "http", "https":
		req := (&http.Request{Method: http.MethodGet, URL: u, Header: make(http.Header)}).WithContext(ctx)

		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("openapi: fetching: %w", err)
		}

		if res.StatusCode != http.StatusOK {
			_ = res.Body.Close()
			return nil, fmt.Errorf("openapi: GET %s: %s", u, res.Status)
		}

		r = res.Body
	case "", "file":
		f, err := os.Open(filepath.FromSlash(u.Path))
		if err != nil {
			return nil, fmt.Errorf("openapi: opening the file: %w", err)
		}

		r = f
	default:
		return nil, fmt.Errorf("openapi: expected a file or an http(s) URL, not %q", u)
	}

	defer r.Close()

	data, err := io.ReadAll(io.LimitReader(r, MaxSpecSize+1))
	if err != nil {
		return nil, fmt.Errorf("openapi: reading %s: %w", u, err)
	}

	if len(data) > MaxSpecSize {
		return nil, fmt.Errorf("%w: %s holds more than %d bytes", ErrTooLarge, u, MaxSpecSize)
	}

	return data, nil
}

// Detect returns the version a spec in JSON or YAML declares: its openapi field, such as 3.1.0, or its swagger field,
// such as 2.0. It fails with [ErrNotOpenAPI] when it declares neither.
func Detect(data []byte) (string, error) {
	// Nodes keep a version as written: swagger: 2.0 is a number to YAML.
	var head struct {
		OpenAPI yaml.Node `yaml:"openapi"`
		Swagger yaml.Node `yaml:"swagger"`
	}

	if err := yaml.Unmarshal(data, &head); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotOpenAPI, err)
	}

	for _, n := range []yaml.Node{head.OpenAPI, head.Swagger} {
		if n.Kind == yaml.ScalarNode && n.Value != "" {
			return n.Value, nil
		}
	}

	return "", fmt.Errorf("%w: expected an openapi or swagger field with its version, such as openapi: 3.1.0", ErrNotOpenAPI)
}
