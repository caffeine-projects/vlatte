# Request matching

They read the call's metadata and its request, as protojson writes it, with the field names of the `.proto` file: enums by name, `int64` values as text, timestamps in RFC 3339.

| Matcher | Matches |
|---|---|
| `Header(key, m)` | A metadata key. |
| `Field(path, m)` | The request at a JSON path, such as `user_id` or `items[0].key`. A client stream's request is the array of its messages: `$[0].key`. |
| `Message(msg)`, `Messages(msgs...)` | A request equal to `msg` by `proto.Equal`; a client stream's messages, in order. |
| `JSON(text)` | A request equal to the message `text` holds, read as protojson reads it, so `{"count": 42}` and `{"count": "42"}` match the same call. |
| `Body(m)` | The request as JSON text, such as `match.Contain("ann")`. |
| `CallMatches(fn)` | A call `fn` reports true for. |
| `Matches(m)` | A call `m` matches: it gets the `*mockgrpc.Call`. |

A client streaming call matches once the client closed its side, on all its messages. A bidi call matches as it starts, on its method and metadata: its `Bidi` reply matches each message.

```go
m.MustMock(mockgrpc.Method("users.Users/Get").
	Header("authorization", match.HasPrefix("Bearer ")).
	Field("id", match.RegExpr("^u-")).
	Reply(mockgrpc.JSON(`{"id": "u-1", "name": "ann"}`)))
```

The matchers they take, such as `match.Equal`, `match.RegExpr` or `match.JSONPath`, are shared by every protocol: see [Matchers](/matchers/). In a [mock file](/grpc/mock-files), `request.header`, `request.fields`, `request.message`, `request.messages`, `request.body` and `request.custom` match the same parts.

## Parsed requests

A request message can carry what the mock should match inside it, such as an inner message sealed in an envelope. `Setup().RequestParsers(parsers...)` registers parsers that read it. A `mockgrpc.RequestParser` has two methods: `CanParse(call, msg)` reports whether it reads a message, and `Parse(ctx, call, msg)` returns what the message holds. For each message, the server asks its parsers in order, and the first that can parse it does. A message no parser can parse is read as the client sent it.

`Parse` returns a `proto.Message`, read as protojson writes it, or any value `encoding/json/v2` writes, such as a map or a `jsontext.Value`, read as that JSON. A nil value reads as `null`.

```go
type sealed struct{ key cipher.AEAD }

func (s sealed) CanParse(call *mockgrpc.Call, _ proto.Message) bool {
	return call.Method() == "/payments.Payments/Pay"
}

func (s sealed) Parse(_ context.Context, _ *mockgrpc.Call, msg proto.Message) (any, error) {
	plain, err := s.key.Open(nil, nil, msg.(*pb.Envelope).GetCiphertext(), nil)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "bad ciphertext")
	}

	inner := new(pb.PayRequest)
	if err := proto.Unmarshal(plain, inner); err != nil {
		return nil, status.Error(codes.InvalidArgument, "not a PayRequest")
	}

	return inner, nil
}

m := mockgrpc.MustNew(mockgrpc.Setup().RequestParsers(sealed{key: aead}))
m.MustMock(mockgrpc.Method("payments.Payments/Pay").
	Field("payee", match.Equal("acme")).
	Reply(mockgrpc.Message(&pb.PayResponse{Ok: true})))
```

| Reads what the parsers read | Reads the message as the client sent it |
|---|---|
| `Field`, `Body`, the rules of `Bidi().On`, the `.Request.Message` of templates, `call.Parsed()`; `request.fields`, `request.body` and `bidi.on.when` of mock files. | `Message`, `Messages`, `JSON`, `call.Message()`, `call.Messages()`, `call.JSON()`, proxies, `Echo`, the journal, recordings and `VerifyRequests().Body`. |

The server parses the request of a unary or server streaming call, and every message of a client streaming call, before it matches the call. A bidi call matches before any message: `call.Recv()` parses each message as it comes, before its `Bidi` rule is chosen, while proxies and `Echo` forward bidi messages unparsed. Each message is parsed once for each call, however many mocks read it.

An error from the parser fails the call, and no other parser is tried. An error with a gRPC status, such as one from `status.Error(codes.InvalidArgument, "bad ciphertext")`, is the answer the client gets, as from a real server that rejects the message. Any other error, or a panic, even one as the parser's value is written as JSON, answers `Internal` with the `vlatte-error` trailer `match`, and no mock counts the call. A bidi message is parsed after its mock matched: a status is the mock's answer, and the mock counts the call, while any other error gets the trailer `reply`. A parser's error never ends the client's side nor cuts the call, even one that wraps `io.EOF`, `vlatte.ErrAbort` or `vlatte.ErrReset`: a `Func` reply that reads `call.Recv()` checks `mockgrpc.ErrParsing` before `io.EOF`. The client reads the error's text, so keep keys and decrypted text out of it.

One parser serves every call at once. It also parses journal entries again, once their method and header pass, when `VerifyRequests` checks them and the admin API searches them. It must be safe for concurrent use, and read a message the same way each time. Read the call's metadata with `call.Header()`, not from the context, because the call of a journal entry has only its header. A journal entry that failed to parse misses every pattern that reads the request. A request the journal cut short is never parsed.

Unlike HTTP body parsers, whose errors only log a warning, a gRPC parser's error fails the call. The `vlatte` command has no parsers: its mock files read every message as the client sent it.
