# Troubleshooting

Use status codes, diagnostic metadata and the request journal to find why a call did not get its expected reply.

## Failures

A gRPC status says how the call ended. The `vlatte-error` trailer identifies an answer from vlatte itself and says what failed:

| What happened | Status | `vlatte-error` |
|---|---|---|
| No mock matched. | `NotFound`, unless `Setup().NoMatchCode` sets another. | `no-match` |
| No descriptor describes the method. | `Unimplemented` | `no-match` |
| A matcher or the mock store failed. | `Internal` | `match` |
| A reply failed, such as a template or a call past the end of a `Seq`. | `Internal` | `reply` |
| Serving the call panicked. | `Internal` | `panic` |

A mock that answers with a status, as `Status(...)` does, gets no `vlatte-error` trailer: that status is its answer. Check the trailer to distinguish an intentional mock error from a failure to serve the mock.

For a unary call that no mock matches, with the default no-match code:

```go
var trailer metadata.MD
_, err := client.SayHello(ctx, &pb.HelloRequest{Name: "ann"}, grpc.Trailer(&trailer))

fmt.Println(status.Code(err))            // NotFound
fmt.Println(trailer.Get("vlatte-error")) // [no-match]
```

`metadata` and `status` are `google.golang.org/grpc/metadata` and `google.golang.org/grpc/status`. For a stream, read `stream.Trailer()` after `Recv` returns an error, including `io.EOF`, or after `CloseAndRecv` returns.

## Debug metadata

A gRPC server sends `vlatte-matched-mock-id`, `vlatte-matched-mock-name` and `vlatte-matched-mock-hits` as header metadata, with a mock's message or status, and `vlatte-request-id` when it keeps a journal. `Name(...)` on a mock builder sets the name. `mockgrpc.Setup().NoDebugHeaders()` turns them off.

Keep a [journal](/more/journal#in-grpc) to inspect the request's metadata, message and outcome, and use its request ID to find the call through the [admin API](/admin-api/). [Request verification](/verify/) reports the closest requests when an expected call didn't arrive.

## Shutdown

`Close` waits for the calls in flight up to `Setup().ShutdownTimeout(d)`, 5s by default, then cuts them.
