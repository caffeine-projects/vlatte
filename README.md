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

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).
- **On start,** the server loads the files matching `testdata/_mocks/*mock.yaml` and `testdata/_mocks/*mock.yml`, in name order. Change the patterns with `Setup().MockFilePatterns(...)`.
- **To load one file,** use `m.MustMock(mockhttp.FromFile(path))`.
- **`m.Reload()`** replaces the mocks of the files with what they hold now.

```yaml
# testdata/_mocks/hello_mock.yaml
id: hello            # optional: names the mock, as in the admin API's /mocks/hello
request:
  method: GET
  path: /hello

response:
  status: 200
  header:
    content-type: text/plain
  body_file: hello.txt   # relative to this file
```

[`mockhttp/schema/http.schema.json`](mockhttp/schema/http.schema.json) describes the format, so editors can validate mock files.

**Types.** Each key takes one type, as the schema says. Write text in quotes where a string goes, as in `name: "123"`, and numbers bare, as in `priority: 10`. Bools read the way Go's `strconv.ParseBool` reads them, so `true`, `"true"`, `t` and `1` are all true. A value of the wrong type is an error that says how to write it.

**Durations.** `delay` and `proxy.timeout` take whole milliseconds (`1500`) or a Go duration (`1.5s`). `"1500"` in quotes is neither.

**Matching.** Most of a request is text: its path, query, headers and form values. A matcher reads that text as the type it compares with:
- `[equal, 10]` and `[isIn, [10, 20]]` match `?page=10`;
- `[gt, 18]` matches `?age=30`;
- `[equal, true]` matches `?debug=1`.

No other reading happens: `[equal, 10]` doesn't match `010`. The [match package docs](match/doc.go) state the rule.

**Keys.** Keys are kept as written, so a query name like `userId` matches `userId` only.

**Templates.** A mock file is a Go template first. It can read the server's parameters, data and URL, as in `{{ .URL "/callback" }}`. So a template of the reply must be escaped: ``{{`{{ .Request.Method }}`}}``.

**Errors.** A file with problems isn't loaded. The error lists every problem with its key and a fix:

```
mock: 2 problems
  file testdata/_mocks/users_mock.yaml: unknown_key: response.stauts is not a known setting; did you mean response.status?
  file testdata/_mocks/users_mock.yaml: conflict: proxy is set with response; keep one of them
```

## Configuration

A server's settings come from these sources, each overriding the ones before it:

1. Defaults.
2. Code: the options passed to `mockhttp.New`, such as `mockhttp.Setup().Addr(":8080")`.
3. A `.vlatterc.json`, `.vlatterc.yaml` or `.vlatterc.yml` file in the working directory or in `testdata`. `--config` or `VLATTE_CONFIG` names another file.
4. `VLATTE_*` environment variables.
5. Command-line flags, for the `vlatte` command.

Each setting has a key that its variable and flag derive from: `log.level` is `log: {level: debug}` in a file, `VLATTE_LOG_LEVEL=debug` and `--log-level=debug`. `vlatte --help` lists every setting.

In a file, each setting has its type, as in mock files: `addr: ":8080"` in quotes, `max_age: 150` bare, and a list as a list. Environment variables and flags are text:
- bools read as `strconv.ParseBool` reads them;
- numbers are written in the JSON number grammar;
- lists are comma-separated.

`log.level` and `log.verbosity` take a name, in any case, such as `info`.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
proxy:
  timeout: 1.5s # or milliseconds: 1500
cors: true
```

Setting a key of a section (`cors`, `proxy` or `record`) turns the section on, and `false` turns it off.

Unknown keys, flags and `VLATTE_*` variables are errors, except `VLATTE_DOCKER_HOST`. A server that can't load its settings reports every problem at once:

```
server: error loading configuration sources
config: 2 problems
  env VLATTE_LOGLEVEL: unknown_key: VLATTE_LOGLEVEL is not a known setting; did you mean VLATTE_LOG_LEVEL?
  file .vlatterc.yaml: invalid_value: proxy.timeout must be milliseconds or a Go duration, not "10x"; such as 1500 or 1.5s
```

Servers in tests read the file and the environment too. Pick the sources with `ConfigSources`, or pass none to use code only:

```go
m := mockhttp.MustNew(mockhttp.Setup().ConfigSources())
m = mockhttp.MustNew(mockhttp.Setup().ConfigSources(mockhttp.ConfigFile("testdata/server.yaml")))
```

## CLI

`vlatte` serves the mock files under `testdata/_mocks` in the working directory:

```sh
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/hello
```

Without `--addr`, it listens on a random port and prints the URL. Settings can also come from a `.vlatterc` file or `VLATTE_*` environment variables; see [Configuration](#configuration). Run `vlatte --help` to list them, and `vlatte completion --help` to set up shell completion.

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
