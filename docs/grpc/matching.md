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
