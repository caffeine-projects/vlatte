# Replies

| Reply | Answers |
|---|---|
| `Message(msg)` | A message, which must be of the method's output type. |
| `Messages(msgs...)` | The messages of a server or bidi stream, in turn. |
| `JSON(texts...)` | The messages `texts` hold, in the method's output type. |
| `Template(texts...)` | Like `JSON`, rendered for each call: `{"message": "hi {{ .Request.Message.name }}"}`, with `.Request.Header`, `.Parameters`, `.Scenarios.State "name"`, `.Scenarios.StateOf "name" "key"`, `.Data`, which `m.SetData` sets, and `.Provided.<key>`, which `Setup().TemplateDataProviders(...)` provide. |
| `Status(code, msg)` | A status, with no message. |
| `Bidi()` | A bidi stream, message by message: `On(matcher, reply)`, `Otherwise(reply)`, `OnEnd(reply)`. |
| `Seq(replies...)` | Each reply in turn, one per call, a stream as a whole, then `OnSequenceEnded(reply)`'s. A call past the end without it fails with the `vlatte-error` trailer. |
| `Rand(replies...)` | One of the replies at random for each call. `RandWithSeed(seed, replies...)` picks in an order the seed repeats, and `RandWith(r, replies...)` with a `*rand.Rand` of yours. |
| `Echo()` | The client's messages back, read as the method's output type by field number: a unary or server streaming call's request, each message of a bidi call as it comes, the last one of a client stream. `Log()` logs each. |
| `Shared(replier)` | The same `Replier` for every mock built with it, which they never close. |
| `Func(fn)` | Whatever `fn` sends through the call, for anything else. |
| `From(target)` | The answer of another gRPC server: see **Proxy replies** below. |

A reply's `Header` and `Trailer` add metadata, `Status` ends a stream with a status after its messages, `Details` adds `errdetails` messages to it, and `MessageDelay` waits before each message. `Build` checks a reply against the method: its messages' type, and that a unary or client streaming method gets one message, or a status. It checks each reply of `Seq` and `Rand` the same way, and each mock built with them goes through its replies on its own.

```go
m.MustMock(mockgrpc.Method("Users/Get").
	Reply(mockgrpc.Status(codes.InvalidArgument, "bad id").
		Details(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "id"}}})))

m.MustMock(mockgrpc.Method("Feed/Watch").
	Reply(mockgrpc.Messages(&pb.Event{Id: "1"}, &pb.Event{Id: "2"}).
		MessageDelay(100 * time.Millisecond).
		Status(codes.Unavailable, "restarting")))

m.MustMock(mockgrpc.Method("Chat/Talk").Reply(mockgrpc.Bidi().
	On(match.Field("text", match.Equal("hi")), mockgrpc.Message(&pb.Line{Text: "hello"})).
	Otherwise(mockgrpc.JSON(`{"text": "?"}`)).
	OnEnd(mockgrpc.Message(&pb.Line{Text: "bye"}))))
```

## Sequences and random replies

`Seq(replies...)` answers each call with the next reply, then the reply of `OnSequenceEnded`; `Rand(replies...)` with one at random. In a [mock file](/grpc/mock-files), `response_sequence` and `response_random`, as for [HTTP](/http/replies#sequences).

## Proxy replies

`From(target)` answers with what another gRPC server answers: see [Proxying](/grpc/proxying).
