# HTTP

`mockhttp` runs HTTP mock servers for tests, and is the server the `vlatte` command runs.

```go
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

`mockhttp.New` does the same as `MustNew` but returns an error instead of panicking. Pass `mockhttp.Setup()` to either one to configure the server: its address, TLS, CORS, proxying or recording, for example.

## In this section

- [Mocks](/http/mocks): building mocks and adding them to a server.
- [Request matching](/http/matching): what a mock can match in a request.
- [Replies](/http/replies): statuses, headers, bodies, sequences and functions.
- [Forms and multipart](/http/forms-multipart): matching uploads and replying with multipart bodies.
- [SSE (Server-sent Events)](/sse/): replying with event streams.
- [Proxying](/http/proxying): proxy replies, proxy all, the forward proxy and HTTPS interception.
- [Steps](/http/steps) and [Faults](/http/faults): changing, slowing and breaking answers.
- [Recording](/recording/http): recording a real service as mock files.
- [Mock files](/http/mock-files): the YAML and JSON format.
- [TLS](/http/tls) and [CORS](/http/cors).
- [Troubleshooting](/http/troubleshooting): failures, near misses and debug headers.

What HTTP shares with gRPC, such as [matchers](/matchers/), [scenarios](/scenarios/), [templates](/templating/), [webhooks](/webhooks/) and the [journal](/more/journal), works the same for both: each has a section of its own in the sidebar.
