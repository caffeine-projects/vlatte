# Proxying

## Proxy replies

`From(target)` answers each call with the answer of another gRPC server, of any call type: the target gets the client's metadata and messages, and the client gets the target's header, messages, trailer and status as they come, a bidi call message by message. `Header` sets metadata the target gets in place of the client's, `RemoveHeaders` leaves some out, and `DialOptions` sets options of the connection, which goes without TLS by default, such as `grpc.WithTransportCredentials`. A target's status is the mock's answer; a target it can't reach fails the call, with the `vlatte-error` trailer.

```go
m.MustMock(mockgrpc.Method("users.Users/Get").Reply(mockgrpc.From("localhost:50051").Header("authorization", "Bearer test")))
```

## Outbound rules

`grpc.outbound.allow` and `grpc.outbound.deny` limit the addresses proxy replies and the [recording](/recording/grpc) target connect to: see [Outbound rules](/http/proxying#outbound-rules).
