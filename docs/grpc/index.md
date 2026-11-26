# gRPC

`mockgrpc` runs gRPC mock servers for tests, and for the `vlatte` command.

A gRPC mock server serves any method of the services whose generated code the program imports: it needs no implementation registered. A mock names its method, matches the calls, and answers them, whatever their type: unary, server streaming, client streaming or bidi streaming.

Here, `pb` is the generated code for a `greeter.proto` such as the one in [examples/grpc-greeter](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-greeter). Use your own service's generated code in its place.

```go
import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"

	"example.com/greeter/pb"
)

func TestGreeter(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
		Field("name", match.HasPrefix("a")).
		Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi ann"})))

	client := pb.NewGreeterClient(m.MustDial())
	res, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "ann"})

	require.NoError(t, err)
	require.Equal(t, "hi ann", res.GetMessage())
}
```

`Setup().InMemory()` serves on an in-memory listener, with no port: only `m.Dial()` reaches it, and `m.Close()` closes the connections it returns. Without it, the server listens on a local port, which `m.Addr()` names, for clients of any language.

## Runnable examples

The [parcel tracking example](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-rpc-types) compares unary, server-streaming, client-streaming and bidirectional calls. Each exchange runs with both Go and YAML mocks. Its README also shows how to call the files with `grpcurl`.

| Example | What you can try |
|---|---|
| [Proxying](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-proxying) | Answer locally or forward a call and its metadata to an upstream. |
| [Recording and replay](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-recording) | Record a live exchange, then serve it after the upstream shuts down. |
| [TLS and mutual TLS](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-tls) | Test server trust and required client certificates. |
| [Scenarios](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-scenarios) | Advance and reset the state of individual parcels. |
| [Reply strategies](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-replies) | Return a sequence, repeat seeded choices, or echo a bidi stream. |
| [Faults](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-faults) | Cancel a delayed stream or cut its connection after one update. |
| [Verification](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-verification) | Count matched and unmatched calls in the journal. |
| [Admin API](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-admin) | Load and toggle a mock over HTTP while the gRPC client is running. |
| [Extensions](https://github.com/caffeine-projects/vlatte/tree/main/examples/grpc-extensions) | Match a parsed request, render a reply, and observe completion. |

## Mock features

As HTTP mocks, gRPC mocks have an `ID`, a `Name` and a `Priority`, can start `Enabled(false)`, serve a number of calls with `Times` and `Once`, take part in [scenarios](/scenarios/) with `StartScenario`, `ScenarioIs`, `ScenarioStateIs`, `ScenarioStateWillBe` and `ScenarioKey`, wait before replying with `Delay`, and run [on_complete events](/webhooks/), such as webhooks, with `OnCompleteEvent`, and callbacks with `Callback`. An answer with a status counts as the mock's answer, for `Times` too. A scenario moves when its mock is picked to answer, before it answers.

See [Mock lifecycle](/more/mock-lifecycle), [Scenarios](/scenarios/) and [Webhooks and events](/webhooks/).

## The server

A gRPC server keeps a [journal](/more/journal#in-grpc) of its calls with `Setup().Journal(dir)`, which tests [verify](/verify/) as HTTP tests do, serves an [admin API](/admin-api/) with `Setup().Admin(admin.MustNew())`, whose mocks `Setup().AdminStore(mockgrpc.FileStore(dir))` [keeps](/admin-api/#persistent-admin-mocks) across restarts, and answers a call no mock matched with `Setup().NoMatchCode(code)`, `NotFound` by default. `Setup().ServerOptions(...)` passes options to the `grpc.Server`, such as interceptors.

## In this section

- [Methods](/grpc/methods): naming the method a mock answers.
- [Request matching](/grpc/matching): metadata, fields and messages.
- [Replies](/grpc/replies): messages, statuses, sequences and functions.
- [Streaming](/grpc/streaming): server, client and bidi streams.
- [Cuts](/grpc/cuts): a server that dies midway.
- [Descriptors and reflection](/grpc/descriptors): serving services without their generated code.
- [Proxying](/grpc/proxying): answers of another gRPC server. [Recording](/recording/grpc) has a section of its own.
- [Mock files](/grpc/mock-files): the YAML and JSON format.
- [TLS](/grpc/tls).
- [Troubleshooting](/grpc/troubleshooting): no-match codes, trailers and debug metadata.

What gRPC shares with HTTP, such as [matchers](/matchers/), [scenarios](/scenarios/), [templates](/templating/#in-grpc), [webhooks](/webhooks/#in-grpc) and the [journal](/more/journal#in-grpc), works the same for both: each has a section of its own in the sidebar.
