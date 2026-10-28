# Steps

Steps are HTTP only, and Go only: in [mock files](/http/mock-files), `steps`, `wire_steps` and `upstream_steps` list [faults](/http/faults#in-mock-files).

In Go, `Step` passes the body of each answer of a mock through steps, in order, before it reaches the client: to delay it, send it in parts, change it or cut it. A step is a `vlatte.Step`. For each answer, its `Open` makes the step's stage of that answer's `vlatte.Flow`, so a stage keeps what it counts for one answer only: a mock's answers don't share it.

```go
upper := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
	if _, err := next.Write(bytes.ToUpper(b)); err != nil {
		return 0, err
	}

	return len(b), nil
})

m.MustMock(mockhttp.Getf("/hello").Step(upper).Reply(mockhttp.OK().PlainText("hello")))
```

## Stages

A stage gets each write of the body, each flush, and the end of the answer, an empty body included. It can pass a write on as it is, changed, in parts, later or not at all, and send what it holds at a flush or at the end. It may write from a goroutine of its own, and needs no lock: vlatte orders its writes with the reply's. Steps can change the body's length, so the answer has no `Content-Length`. A stage aborts the answer by returning an error that matches `vlatte.ErrAbort`, or with `Flow.Abort`, such as from a timer, which ends a reply that waits on the answer's context, as a proxy reply does. The mock still counts the call and runs its callbacks and events. Then the connection is cut, after what was sent: an HTTP/1 connection is closed, and an HTTP/2 stream is reset. With `vlatte.ErrReset`, an HTTP/1 connection is reset too, so the client reads `ECONNRESET`. A failure, such as a failing reply, is vlatte's answer, and skips the steps.

## Wire steps

`WireStep` passes every byte of each answer through its steps, after those of `Step`: the status line, the headers, the framing of the body and the body, so a step can send what isn't HTTP, such as nothing at all, half the headers, or garbage after a valid head. The mock takes its connection over and answers in HTTP/1.x itself, with `Connection: close`: the body is chunked over HTTP/1.1, and ends with the connection over HTTP/1.0. A request over HTTP/2 can't give its connection over, so it gets vlatte's error answer: turn HTTP/2 off, or use an HTTP/1.1 client, to test a mock with wire steps. An abort closes the connection without the rest of the answer, and `vlatte.ErrReset` resets it, once the mock counted the call and ran its callbacks and events. A recording leaves out the answers of mocks with wire steps.

## Upstream steps

`UpstreamStep` passes what a mock sends on through its steps: the body of the request a proxy reply sends to its target. Steps can change its length, so the target gets it chunked. The server reads the body of a request before it matches a mock, so these steps don't slow the client's upload: they apply to what the mock sends on. A body the server doesn't read whole, with `NoBodyParsing` or past `MaxBodyParsingLimit`, is read from the client as the target reads it, a small piece at a time, so a step that slows it slows the upload too, once the connection's buffers are full. A mock with wire steps takes its connection over before it replies, so it reads such a body through these steps first: they pace the upload, and a proxy's target gets the body as they passed it on, at once. A stage that aborts ends the call to the target, and the client's answer is cut as for any abort.

## Switched protocols

A connection that switched protocols, such as a WebSocket, goes through the steps too. The answer ends with its head, so its steps end there, and a stage that cuts it leaves the client without a switch. Then what the target sends the client passes the mock's steps again, in a flow of its own, then its wire steps, which saw the `101` too. What the client sends the target passes its upstream steps. A stage that aborts either way, even on a timer while nothing comes, cuts both sides, or resets them with `vlatte.ErrReset`.
