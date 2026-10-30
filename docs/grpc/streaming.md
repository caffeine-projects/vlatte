# Streaming

A mock answers its method whatever its call type. `Build` checks a reply against it: a unary or client streaming method gets one message, or a status; a server or bidi streaming method gets any number of messages.

## Server streaming

`Messages(msgs...)` sends the messages of the stream in turn. `MessageDelay(d)` waits before each message, and `Status(code, msg)` ends the stream with a status after its messages.

```go
m.MustMock(mockgrpc.Method("Feed/Watch").
	Reply(mockgrpc.Messages(&pb.Event{Id: "1"}, &pb.Event{Id: "2"}).
		MessageDelay(vlatte.Latency(100 * time.Millisecond)).
		Status(codes.Unavailable, "restarting")))
```

The client reads the two events, then `Unavailable`.

## Client streaming

A client streaming call matches once the client closed its side, on all its messages. Its request is the array of its messages: `Field("$[0].key", m)` reads the first, and `Messages(msgs...)` matches them all, in order.

```go
m.MustMock(mockgrpc.Method("Upload/Send").
	Field("$[0].name", match.Equal("first")).
	Reply(mockgrpc.JSON(`{"count": 2}`)))
```

`Echo()` answers a client stream with its last message, read as the method's output type by field number.

## Bidi streaming

A bidi call matches as it starts, on its method and metadata. Its `Bidi()` reply then matches each message the client sends:

| Method | Answers |
|---|---|
| `On(matcher, reply)` | A message the matcher matches, with the reply's messages. Rules are tried in order. |
| `Otherwise(reply)` | A message no rule matches. |
| `OnEnd(reply)` | The end of the client's side, with the reply's messages, before the stream ends. |

```go
m.MustMock(mockgrpc.Method("Chat/Talk").Reply(mockgrpc.Bidi().
	On(match.Field("text", match.Equal("hi")), mockgrpc.Message(&pb.Line{Text: "hello"})).
	Otherwise(mockgrpc.JSON(`{"text": "?"}`)).
	OnEnd(mockgrpc.Message(&pb.Line{Text: "bye"}))))
```

The client that sends `hi`, then `what`, then closes its side reads `hello`, `?` and `bye`.

`Echo()` answers each message of a bidi call as it comes. In a [mock file](/grpc/mock-files), `bidi` takes `header`, `trailer`, `on` (rules of `when`, a matcher list of the message, and `response`), `otherwise` and `on_end`.

```yaml
request:
  method: chat.Chat/Talk
bidi:
  on:
    - when: [field, text, [eq, hi]]
      response:
        message:
          text: hello
  otherwise:
    message:
      text: "?"
  on_end:
    message:
      text: bye
```

## Cutting a stream

A `Func` reply that returns `vlatte.ErrAbort` or `vlatte.ErrReset` cuts the call after the messages it sent: see [Cuts](/grpc/cuts).
