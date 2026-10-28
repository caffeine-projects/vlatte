# Quick start

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
curl http://127.0.0.1:8080/hello
```

## Next

- [Concepts](/getting-started/concepts): mocks, matchers, replies and servers.
- [HTTP](/http/) and [gRPC](/grpc/): everything each protocol does.
- [Guides](/guides/): longer walkthroughs.
