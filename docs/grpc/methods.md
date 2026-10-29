# Methods

`Method` takes a method by whole segments of its full method: `SayHello`, `Greeter/SayHello`, `greeter.Greeter/SayHello`, or the full method, such as `pb.Greeter_SayHello_FullMethodName`. A mock fails to build, rather than wait for calls that never come, when its name names no method (`mockgrpc.ErrUnknownMethod`: a typo), or methods of different call types (`mockgrpc.ErrAmbiguousMethod`: name the service). A call of a method no descriptor describes answers `Unimplemented`.

```go
m.MustMock(mockgrpc.Method("SayHello").Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi"})))
m.MustMock(mockgrpc.Method(pb.Greeter_SayHello_FullMethodName).Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi"})))
```

## Call types

A mock answers its method whatever its call type: unary, server streaming, client streaming or bidi streaming. A reply is checked against it when the mock is built: see [Replies](/grpc/replies) and [Streaming](/grpc/streaming).
