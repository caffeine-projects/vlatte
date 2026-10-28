# Cuts

A reply that returns an error matching `vlatte.ErrAbort` or `vlatte.ErrReset`, such as a `Func` reply, cuts the call, to test a client against a server that dies midway. The mock counts the call and runs its events, then the server cuts the call's connection after the messages of a stream the reply sent, before any status: the client reads `Unavailable`, and no trailer. `vlatte.ErrReset` resets a TCP connection, so the client reads `ECONNRESET`, and `vlatte.ErrAbort` ends it. grpc can't reset one stream from a handler, so the other calls on the connection end too. A server whose `ServerOptions` set `grpc.Creds` can't cut its connections: the call fails, with the `vlatte-error` trailer.

```go
m.MustMock(mockgrpc.Method("Feed/Watch").Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
	if err := call.Send(&pb.Event{Id: "1"}); err != nil {
		return err
	}

	return vlatte.ErrReset // after event 1, the client reads a reset
})))
```
