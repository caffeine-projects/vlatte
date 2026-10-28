# gRPC

`mockgrpc` runs gRPC mock servers for tests, and for the `vlatte` command.

A gRPC mock server serves any method of the services whose generated code the program imports: it needs no implementation registered. A mock names its method, matches the calls, and answers them, whatever their type: unary, server streaming, client streaming or bidi streaming.

```go
func TestGreeter(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory()) // no port: only m.Dial reaches it
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
		Header("x-team", match.Equal("core")).
		Field("name", match.Equal("ann")).
		Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi ann"})))

	client := pb.NewGreeterClient(m.MustDial()) // closed with the server
	res, err := client.SayHello(metadata.AppendToOutgoingContext(t.Context(), "x-team", "core"), &pb.HelloRequest{Name: "ann"})
	// ...
}
```

`Setup().InMemory()` serves on an in-memory listener, with no port: only `m.Dial()` reaches it. Without it, the server listens on a local port, which `m.Addr()` names, for clients of any language.

## Mock features

As HTTP mocks, gRPC mocks have an `ID`, a `Name` and a `Priority`, can start `Enabled(false)`, serve a number of calls with `Times` and `Once`, take part in [scenarios](/features/scenarios) with `StartScenario`, `ScenarioIs`, `ScenarioStateIs`, `ScenarioStateWillBe` and `ScenarioKey`, wait before replying with `Delay` and `DelayFunc`, and run [on_complete events](/features/webhooks-events), such as webhooks, with `OnCompleteEvent`, and callbacks with `Callback`. An answer with a status counts as the mock's answer, for `Times` too. A scenario moves when its mock is picked to answer, before it answers.

See [Mock lifecycle](/features/mock-lifecycle), [Scenarios](/features/scenarios) and [Webhooks and events](/features/webhooks-events).

## The server

A gRPC server keeps a [journal](/features/journal#in-grpc) of its calls with `Setup().Journal(dir)`, which tests verify with the same builders, serves an [admin API](/run/admin-api) with `Setup().Admin(admin.MustNew())`, whose mocks `Setup().AdminStore(mockgrpc.FileStore(dir))` [keeps](/run/admin-api#persistent-admin-mocks) across restarts, and answers a call no mock matched with `Setup().NoMatchCode(code)`, `NotFound` by default. `Setup().ServerOptions(...)` passes options to the `grpc.Server`, such as interceptors.

## In this section

- [Methods](/grpc/methods): naming the method a mock answers.
- [Request matching](/grpc/matching): metadata, fields and messages.
- [Replies](/grpc/replies): messages, statuses, sequences and functions.
- [Streaming](/grpc/streaming): server, client and bidi streams.
- [Cuts](/grpc/cuts): a server that dies midway.
- [Descriptors and reflection](/grpc/descriptors): serving services without their generated code.
- [Proxying and recording](/grpc/proxying-recording).
- [Mock files](/grpc/mock-files): the YAML and JSON format.
- [TLS](/grpc/tls).
- [Troubleshooting](/grpc/troubleshooting): no-match codes, trailers and debug metadata.

What gRPC shares with HTTP, such as [matchers](/features/matchers), [scenarios](/features/scenarios), [templates](/features/templates#in-grpc), [webhooks](/features/webhooks-events#in-grpc) and the [journal](/features/journal#in-grpc), is under [Features](/features/).
