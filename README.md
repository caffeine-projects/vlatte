# vlatte

Mock HTTP and gRPC servers for Go tests, and a CLI that serves HTTP mocks from files.

## Install

Requires Go 1.27 or later.

```sh
go get github.com/caffeine-projects/vlatte
```

The CLI:

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

## Mock HTTP calls in tests

```go
package hello_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestHello(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q", body)
	}

	scoped.AssertCalled(t)
}
```

`mockhttp.New` does the same as `MustNew` but returns an error instead of panicking. Pass `mockhttp.Setup()` to either one to configure the server: its address, TLS, CORS, proxying or recording, for example. The `mockgrpc` package offers the same for gRPC services.

## Packages

| Package | What it's for |
|---|---|
| `mockhttp` | HTTP mock servers |
| `mockgrpc` | gRPC mock servers |
| `match` | Request matchers, such as `match.Equal`, `match.JSONPath` and `match.All` |
| `webhook` | Calling a URL after a mock is served |
| `recipe/<protocol>/<recipe>` | Recipes that prepare a server, such as `recipe/http/proxyall` |
| `vlatte` | The types every mock server shares |

A recipe prepares a server through `Prep`. This one forwards every request that no mock matches:

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(proxyall.Config{Target: "https://api.example.com"}))
```

## Mock files

Mocks can also live in YAML or JSON files. On start, the server loads the files matching `testdata/_mocks/*mock.yaml` and `testdata/_mocks/*mock.yml`. Change the patterns with `Setup().MockFilePatterns(...)`, or load a single file with `m.MustMock(mockhttp.FromFile(path))`.

```yaml
# testdata/_mocks/hello_mock.yaml
request:
  method: GET
  path: /hello

response:
  status: 200
  header:
    content-type: text/plain
  body: hello world
```

[`mockhttp/schema/http.schema.json`](mockhttp/schema/http.schema.json) describes the format, so editors can validate mock files.

## CLI

`vlatte` serves the mock files under `testdata/_mocks` in the working directory:

```sh
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/hello
```

Without `--addr`, it listens on a random port and prints the URL. Options can also come from `VLATTE_`-prefixed environment variables, such as `VLATTE_ADDR`, or from a `.vlatterc.yaml` file in the working directory or in `testdata`. Run `vlatte --help` to list them.

While it runs, `POST /__admin/mocks` adds a mock sent in the request body, for example as JSON:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/added"},"response":{"status":201}}' \
  http://127.0.0.1:8080/__admin/mocks
```

## Development

```sh
make test   # run the tests with the race detector
make lint   # run golangci-lint
```

## License

[MIT](LICENSE)
