# Testing a gRPC streaming client

This guide tests a client of a server stream: it reads every event, handles a stream that ends with an error status, and one whose server dies midway. Then it mocks a bidi chat. It uses [gRPC streaming](/grpc/streaming), [cuts](/grpc/cuts) and [matching](/grpc/matching).

## The service

```proto
syntax = "proto3";
package feed;

message WatchRequest { string topic = 1; }
message Event { string id = 1; }
message Line { string text = 1; }

service Feed {
  rpc Watch(WatchRequest) returns (stream Event);
  rpc Chat(stream Line) returns (stream Line);
}
```

A gRPC mock server serves any method of the services whose generated code the test imports, here `pb`: it needs no implementation.

## The client

`collect` reads the events of a topic until the stream ends:

```go
func collect(ctx context.Context, client pb.FeedClient, topic string) ([]string, error) {
	stream, err := client.Watch(ctx, &pb.WatchRequest{Topic: topic})
	if err != nil {
		return nil, err
	}

	var ids []string
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return ids, nil
		}
		if err != nil {
			return ids, err
		}
		ids = append(ids, event.GetId())
	}
}
```

## A server per test

`InMemory()` serves on an in-memory listener: no port, and `m.Dial()` connects to it. The connection closes with the server.

```go
func newFeed(t *testing.T) *mockgrpc.Server {
	t.Helper()

	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}
```

## Reading every event

`Messages` sends the events in turn, and `MessageDelay` spaces them out:

```go
func TestCollectReadsEveryEvent(t *testing.T) {
	m := newFeed(t)
	m.MustMock(mockgrpc.Method("feed.Feed/Watch").
		Field("topic", match.Equal("orders")).
		Reply(mockgrpc.Messages(&pb.Event{Id: "1"}, &pb.Event{Id: "2"}).
			MessageDelay(10 * time.Millisecond)))

	ids, err := collect(t.Context(), pb.NewFeedClient(m.MustDial()), "orders")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []string{"1", "2"}) {
		t.Errorf("ids = %v", ids)
	}
}
```

## A stream that ends with an error

`Status` ends the stream with a status after its messages. The client keeps what it read, and gets the status:

```go
func TestCollectReturnsTheStatus(t *testing.T) {
	m := newFeed(t)
	m.MustMock(mockgrpc.Method("feed.Feed/Watch").
		Reply(mockgrpc.Messages(&pb.Event{Id: "1"}).Status(codes.Unavailable, "restarting")))

	ids, err := collect(t.Context(), pb.NewFeedClient(m.MustDial()), "orders")
	if status.Code(err) != codes.Unavailable || len(ids) != 1 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}
```

## A server that dies midway

A `Func` reply sends what it wants through the call. Returning `vlatte.ErrReset` cuts the connection after the event it sent, before any status: the client reads `Unavailable`, and no trailer.

```go
func TestCollectSurvivesACut(t *testing.T) {
	m := newFeed(t)
	m.MustMock(mockgrpc.Method("feed.Feed/Watch").Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
		if err := call.Send(&pb.Event{Id: "1"}); err != nil {
			return err
		}

		return vlatte.ErrReset
	})))

	ids, err := collect(t.Context(), pb.NewFeedClient(m.MustDial()), "orders")
	if status.Code(err) != codes.Unavailable || len(ids) != 1 {
		t.Fatalf("ids = %v, err = %v", ids, err)
	}
}
```

## A bidi chat

A bidi call matches as it starts, on its method and metadata. Its `Bidi` reply then answers each message: `On` the messages a matcher matches, `Otherwise` the rest.

```go
m.MustMock(mockgrpc.Method("feed.Feed/Chat").
	Header("x-room", match.Equal("general")).
	Reply(mockgrpc.Bidi().
		On(match.Field("text", match.Equal("ping")), mockgrpc.Message(&pb.Line{Text: "pong"})).
		Otherwise(mockgrpc.Status(codes.InvalidArgument, "unknown command"))))
```

## What you used

- `Setup().InMemory`, `MustDial`: [gRPC](/grpc/).
- `Method`, `Field`, `Header`: [Methods](/grpc/methods), [Request matching](/grpc/matching).
- `Messages`, `MessageDelay`, `Status`, `Bidi`: [Replies](/grpc/replies), [Streaming](/grpc/streaming).
- `Func`, `vlatte.ErrReset`: [Cuts](/grpc/cuts).
