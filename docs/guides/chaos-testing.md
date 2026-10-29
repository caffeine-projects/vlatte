# Chaos testing

This guide checks that a client retries a failing server, survives a reset connection, and gives up on a slow one. It uses [sequences](/http/replies#sequences), [faults](/http/faults) and [priorities](/more/mock-lifecycle).

## The client

`get` retries a `GET` up to three times, on a `5xx` answer or a network error:

```go
// get retries a GET up to three times on a 5xx answer or a network error.
func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	var last error
	for range 3 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		res, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}

		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if res.StatusCode >= 500 {
			last = fmt.Errorf("status %d", res.StatusCode)
			continue
		}

		return body, nil
	}

	return nil, last
}
```

## A server that recovers

`Seq` answers each request with the next reply, then the one of `OnSequenceEnded`: two `503`s, then the report. `AssertNumberOfCalls` checks the client called three times.

```go
func TestRetriesUntilTheServerRecovers(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Getf("/report").Reply(mockhttp.Seq(
		mockhttp.ServiceUnavailable(),
		mockhttp.ServiceUnavailable(),
	).OnSequenceEnded(mockhttp.OK().PlainText("done"))))

	body, err := get(t.Context(), http.DefaultClient, m.URL("/report"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "done" {
		t.Errorf("body = %q", body)
	}

	scoped.AssertNumberOfCalls(t, 3)
}
```

## A reset connection

`fault.ConnectionResetByPeer()` resets the TCP connection before the status line. `Once()` serves it to the first request only; the next request falls through to the mock of priority 1, which answers.

```go
func TestRetriesAResetConnection(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.Getf("/report").Once().WireStep(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()),
		mockhttp.Getf("/report").Priority(1).Reply(mockhttp.OK().PlainText("done")),
	)

	body, err := get(t.Context(), http.DefaultClient, m.URL("/report"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "done" {
		t.Errorf("body = %q", body)
	}
}
```

Wire faults, such as resets, need HTTP/1.x: Go's client uses it against a plain `http://` server.

## A slow server

`fault.Latency` delays every write of the answer. The client gives up after its timeout, and the test checks the error is a timeout:

```go
func TestGivesUpOnASlowServer(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/report").
		WireStep(fault.Latency(500*time.Millisecond, 0)).
		Reply(mockhttp.OK().PlainText("too late")))

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := get(t.Context(), client, m.URL("/report"))

	if timeout, ok := errors.AsType[net.Error](err); !ok || !timeout.Timeout() {
		t.Fatalf("err = %v, want a timeout", err)
	}
}
```

To slow only the body, use `Step` in place of `WireStep`: the client then gets the status and headers at once. `fault.Bandwidth`, `fault.ChunkedDribbleDelay` and `fault.Timeout` shape a slow network in other ways: see [Faults](/http/faults).

## The same in mock files

```yaml
request: {method: GET, path: /report}
response: {status: 200, body: done}
wire_steps:
  - type: latency
    latency: 500ms
```

## What you used

- `Seq`, `OnSequenceEnded`: [Replies](/http/replies#sequences).
- `WireStep`, `fault.ConnectionResetByPeer`, `fault.Latency`: [Steps](/http/steps), [Faults](/http/faults).
- `Once`, `Priority`, `AssertNumberOfCalls`: [Mock lifecycle](/more/mock-lifecycle), [Mocks](/http/mocks#asserting).
