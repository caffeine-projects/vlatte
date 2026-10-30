# Quick start

Choose [HTTP](#http) or [gRPC](#grpc) to embed VLatte in a Go test. For a client in another language, use [the CLI](#the-cli) to serve mock files; a binary or Docker image needs no Go installation.

## HTTP

A test starts a server, adds a mock, and points the code under test at `m.URL(...)`:

<<< @/../mockhttp/example_test.go#example{go}

In a test, use `t.Cleanup(m.Close)` in place of `defer`, and `scoped.AssertCalled(t)` to fail the test when the mock wasn't called:

```go
func TestHello(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	// ... call m.URL("/hello") ...

	scoped.AssertCalled(t)
}
```

`mockhttp.New` does the same as `MustNew` but returns an error instead of panicking. Pass `mockhttp.Setup()` to either one to configure the server: its address, TLS, CORS, proxying or recording, for example.

## gRPC

A gRPC mock server serves any method of the services whose generated code the program imports. `InMemory()` needs no port: `m.Dial()` connects to it.

<<< @/../mockgrpc/example_test.go#example{go}

## The CLI

Use a CLI built from the version of VLatte documented here; see [Installation](/getting-started/installation). For a complete browser workflow without Go, follow [Testing a frontend client](/guides/testing-a-frontend-client).

Write a mock file:

```yaml
# testdata/vlatte/_mocks/hello_mock.yaml
request:
  method: GET
  path: /hello
response:
  status: 200
  body: hello world
```

Serve it:

```sh
vlatte --addr=127.0.0.1:8080
```

Leave the server running, then call it from another terminal:

```sh
curl http://127.0.0.1:8080/hello
# hello world
```

Point your app's development or test API base URL at `http://127.0.0.1:8080`. Browser apps may also need [CORS](/http/cors); a device needs an [address it can reach](/guides/testing-a-frontend-client#connect-a-mobile-app).

## Next

- [Concepts](/getting-started/concepts): mocks, matchers, replies and servers.
- [HTTP](/http/) and [gRPC](/grpc/): everything each protocol does.
- [Guides](/guides/): longer walkthroughs.
