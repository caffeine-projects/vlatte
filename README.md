<p align="center">
  <img src="docs/brand/logo.svg" alt="" width="128" height="128">
</p>

<h1 align="center">VLatte</h1>

<p align="center">
  Mock HTTP and gRPC servers for Go tests.<br>
  Match requests, reply with templates, record and proxy.<br>
  A CLI that serves HTTP and gRPC mocks from files.
</p>

<p align="center">
  <a href="https://github.com/caffeine-projects/vlatte/actions/workflows/ci.yml"><img src="https://github.com/caffeine-projects/vlatte/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://codecov.io/gh/caffeine-projects/vlatte"><img src="https://codecov.io/gh/caffeine-projects/vlatte/graph/badge.svg" alt="codecov"></a>
  <a href="https://pkg.go.dev/github.com/caffeine-projects/vlatte"><img src="https://pkg.go.dev/badge/github.com/caffeine-projects/vlatte.svg" alt="Go Reference"></a>
</p>

## About

VLatte is a mocking framework for Go. In a test, `mockhttp` and `mockgrpc` start real HTTP and gRPC servers that answer the way the test says: match a request on its method, path, headers, query or body, reply with a status, headers and a templated body, call a webhook, or forward what nothing matches to a real upstream.

The `vlatte` CLI serves HTTP and gRPC mocks from YAML or JSON files, records what passes through it and plays it back, and takes new mocks over an admin API.

## Install

Requires Go 1.27 or later.

```sh
go get github.com/caffeine-projects/vlatte
```

The CLI:

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

## Mock HTTP calls in tests

```go
package hello_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestHello(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q", body)
	}

	scoped.AssertCalled(t)
}
```

`mockhttp.New` does the same as `MustNew` but returns an error instead of panicking. Pass `mockhttp.Setup()` to either one to configure the server: its address, TLS, CORS, proxying or recording, for example. The `mockgrpc` package offers the same for gRPC services: see [Mock gRPC calls](#mock-grpc-calls).

## Mock gRPC calls

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

**Methods.** `Method` takes a method by whole segments of its full method: `SayHello`, `Greeter/SayHello`, `greeter.Greeter/SayHello`, or the full method, such as `pb.Greeter_SayHello_FullMethodName`. A mock fails to build, rather than wait for calls that never come, when its name names no method (`mockgrpc.ErrUnknownMethod`: a typo), or methods of different call types (`mockgrpc.ErrAmbiguousMethod`: name the service). A call of a method no descriptor describes answers `Unimplemented`.

**Matchers.** They read the call's metadata and its request, as protojson writes it, with the field names of the `.proto` file: enums by name, `int64` values as text, timestamps in RFC 3339.

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

**Replies.**

| Reply | Answers |
|---|---|
| `Message(msg)` | A message, which must be of the method's output type. |
| `Messages(msgs...)` | The messages of a server or bidi stream, in turn. |
| `JSON(texts...)` | The messages `texts` hold, in the method's output type. |
| `Template(texts...)` | Like `JSON`, rendered for each call: `{"message": "hi {{ .Request.Message.name }}"}`, with `.Request.Header` and `.Parameters`. |
| `Status(code, msg)` | A status, with no message. |
| `Bidi()` | A bidi stream, message by message: `On(matcher, reply)`, `Otherwise(reply)`, `OnEnd(reply)`. |
| `Seq(replies...)` | Each reply in turn, one per call, a stream as a whole, then `OnSequenceEnded(reply)`'s. A call past the end without it fails with the `vlatte-error` trailer. |
| `Rand(replies...)` | One of the replies at random for each call. `RandWithSeed(seed, replies...)` picks in an order the seed repeats, and `RandWith(r, replies...)` with a `*rand.Rand` of yours. |
| `Echo()` | The client's messages back, read as the method's output type by field number: a unary or server streaming call's request, each message of a bidi call as it comes, the last one of a client stream. `Log()` logs each. |
| `Shared(replier)` | The same `Replier` for every mock built with it, which they never close. |
| `Func(fn)` | Whatever `fn` sends through the call, for anything else. |
| `From(target)` | The answer of another gRPC server: see **Proxying and recording** below. |

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

**Cuts.** A reply that returns an error matching `vlatte.ErrAbort` or `vlatte.ErrReset`, such as a `Func` reply, cuts the call, to test a client against a server that dies midway. The mock counts the call and runs its events, then the server cuts the call's connection after the messages of a stream the reply sent, before any status: the client reads `Unavailable`, and no trailer. `vlatte.ErrReset` resets a TCP connection, so the client reads `ECONNRESET`, and `vlatte.ErrAbort` ends it. grpc can't reset one stream from a handler, so the other calls on the connection end too. A server whose `ServerOptions` set `grpc.Creds` can't cut its connections: the call fails, with the `vlatte-error` trailer.

```go
m.MustMock(mockgrpc.Method("Feed/Watch").Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
	if err := call.Send(&pb.Event{Id: "1"}); err != nil {
		return err
	}

	return vlatte.ErrReset // after event 1, the client reads a reset
})))
```

**Mock features.** As HTTP mocks, gRPC mocks have an `ID`, a `Name` and a `Priority`, can start `Enabled(false)`, serve a number of calls with `Times` and `Once`, take part in scenarios with `StartScenario`, `ScenarioIs`, `ScenarioStateIs` and `ScenarioStateWillBe`, wait before replying with `Delay` and `DelayFunc`, and run [on_complete events](#webhooks), such as webhooks, with `OnCompleteEvent`, and callbacks with `Callback`. An answer with a status counts as the mock's answer, for `Times` and scenarios too.

**Descriptors.** A server without the generated code of a service serves it from its descriptors, with messages of dynamic types: `Setup().Descriptors("api.binpb")` loads descriptor sets, as `protoc --descriptor_set_out=api.binpb --include_imports` or `buf build -o api.binpb` write them, and `Setup().Files(files)` takes a `*protoregistry.Files`.

**Mock files.** `Setup().MockFiles("testdata/grpc/*.yaml")` loads gRPC mock files at `Start`, and `m.Reload()` again; `mockgrpc.FromFile` and `FromBytes` build one. Their keys mirror the Go API, and [`mockgrpc/schema/grpc.schema.json`](mockgrpc/schema/grpc.schema.json) describes them for editors:

```yaml
id: denied
priority: 1
request:
  method: users.Users/Get
  header: {authorization: [hasprefix, "Bearer "]}
  fields: {id: [regex, "^u-"]}
response:
  status:
    code: PERMISSION_DENIED
    message: not yours
    details:
      - {"@type": type.googleapis.com/google.rpc.ErrorInfo, reason: NOT_OWNER}
```

`request.message` and `request.messages` match whole messages, `request.body` takes a matcher of the request's JSON, and `request.custom` lists matchers of the whole call, as `Matches` does. `response` takes `message`, `messages`, `body_file` (a JSON file of a message or a list of them), `template`, `header`, `trailer`, `status` and `message_delay`; `bidi` takes `header`, `trailer`, `on` (rules of `when` and `response`), `otherwise` and `on_end`; `proxy` takes `target`, `header` and `remove_headers`. `response_sequence` takes `responses`, a list of `response`s, and `after_ended`, a `response`; `response_random` takes `responses` and `seed`. `delay`, `repeat`, `scenario` and `on_complete` work as in HTTP mock files. `Setup().Matchers(registry)` gives a server the custom matchers its mock files name, as it does an HTTP one: see [Mock files](#mock-files).

**Proxying and recording.** `From(target)` answers each call with the answer of another gRPC server, of any call type: the target gets the client's metadata and messages, and the client gets the target's header, messages, trailer and status as they come, a bidi call message by message. `Header` sets metadata the target gets in place of the client's, `RemoveHeaders` leaves some out, and `DialOptions` sets options of the connection, which goes without TLS by default, such as `grpc.WithTransportCredentials`. A target's status is the mock's answer; a target it can't reach fails the call, with the `vlatte-error` trailer.

```go
m.MustMock(mockgrpc.Method("users.Users/Get").Reply(mockgrpc.From("localhost:50051").Header("authorization", "Bearer test")))

err := m.StartRecording(mockgrpc.RecordTarget("localhost:50051"), mockgrpc.RecordDir("testdata/grpc_recorded"))
// ... calls to m reach the target, and are recorded ...
res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

A gRPC server records as an HTTP one does: see [Recording and playback](#recording-and-playback). With `RecordTarget`, the calls the recording takes go to the target before any mock; without one, it takes the answers of `From` mocks, and of every mock with `RecordIncludeMocked(true)`. `RecordMethods` names the methods it takes, as `Method` does, and `RecordRequestHeaders` the metadata its mocks match. A recording matches a call by its method, its messages and that metadata, and answers with the header, messages, trailer and status of the calls of that request in turn, as a `response_sequence`, then the last one again; `RecordRepeats(vlatte.RecordFirst)` keeps the first only. A bidi call matches by its method and metadata only, as bidi mocks do, and plays its messages back at once. `Setup().Record(...)`, the `grpc.record` section of the settings and the admin API's `/recording` routes record too, and a server serves the recordings of its `grpc.record.dir`, `_mocks_recorded` under its root directory by default.

**More.** `Setup().Reflection()` serves server reflection, so `grpcurl` lists the services and describes their messages. A server keeps a [journal](#request-journal) of its calls with `Setup().Journal(dir)`, which tests verify with the same builders, serves an [admin API](#admin-api) with `Setup().Admin(admin.MustNew())`, and answers a call no mock matched with `Setup().NoMatchCode(code)`, `NotFound` by default. `Close` waits for the calls in flight up to `Setup().ShutdownTimeout(d)`, 5s by default, then cuts them.

**TLS.** `m.StartTLS()` starts the server as `Start` does, serving TLS. `Setup().TLSCertKeyPair(certFile, keyFile)` or `TLSCertificates(certs)` sets its certificate, `TLSMutual(certFile, keyFile, clientCAFile)` or `TLSClientCAs(pool)` requires clients to present a certificate those CAs signed, and `TLSConfig(c)` sets the whole TLS configuration. Without a certificate, it serves a self-signed one for `localhost`, `127.0.0.1` and `::1`, which `m.Certificate()` returns for clients to trust. `m.Dial()` trusts the certificate the server serves; pass it `grpc.WithTransportCredentials` for a server that requires a client certificate. Credentials `ServerOptions` set with `grpc.Creds` win over these: `StartTLS` then serves them as `Start` does, `m.Certificate()` returns `nil`, and `m.Dial()` needs `grpc.WithTransportCredentials`.

## Packages

| Package | What it's for |
|---|---|
| `mockhttp` | HTTP mock servers |
| `mockgrpc` | gRPC mock servers |
| `match` | Request matchers, such as `match.Equal`, `match.JSONPath` and `match.All` |
| `match/matchbuild` | The matcher lists of mock files, and the registry of custom matchers |
| `match/matchscenario` | Scenario state for mock servers, and `scenariotest`, the suite a mock server runs to check it serves scenarios |
| `webhook` | Calling a URL after a mock is served |
| `recipe/<protocol>/<recipe>` | Recipes that prepare a server, such as `recipe/http/proxyall` |
| `vlatte` | The types every mock server shares, and the Go template engine |

A recipe prepares a server through `Prep`. This one forwards every request that no mock matches, with the options of the proxy reply it takes:

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

## Mock files

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).
- **On start,** the server loads the files matching `testdata/vlatte/_mocks/*mock.yaml` and `testdata/vlatte/_mocks/*mock.yml`, and the [recordings](#recording-and-playback) in `testdata/vlatte/_mocks_recorded`, in name order: the folders of its [work directory](#work-directory). Change the patterns with `Setup().MockFilePatterns(...)`.
- **To load one file,** use `m.MustMock(mockhttp.FromFile(path))`.
- **`m.Reload()`** replaces the mocks of the files with what they hold now, in one step. When a file has problems, it returns them and the mocks stay as they were.

```yaml
# testdata/vlatte/_mocks/hello_mock.yaml
id: hello            # optional: names the mock, as in the admin API's /mocks/hello
request:
  method: GET
  path: /hello

response:
  status: 200
  header:
    content-type: text/plain
  body_file: hello.txt   # relative to this file
```

[`mockhttp/schema/http.schema.json`](mockhttp/schema/http.schema.json) describes the format, so editors can validate mock files.

**Types.** Each key takes one type, as the schema says. Write text in quotes where a string goes, as in `name: "123"`, and numbers bare, as in `priority: 10`. Bools read the way Go's `strconv.ParseBool` reads them, so `true`, `"true"`, `t` and `1` are all true. A value of the wrong type is an error that says how to write it.

**Bodies.** A reply's `body` is text as is, and any other value as JSON. A body that isn't UTF-8 text, such as an image or text in another encoding, goes in `body_base64`, as standard base64 (`body_base64: //4A`), whose line breaks are ignored, or in a `body_file`. A reply takes one of `body`, `body_base64`, `body_file` and `multipart` at most.

**Durations.** `delay`, `proxy.timeout`, the durations of [faults](#faults) and the `delay` and `timeout` of a [webhook](#webhooks) take whole milliseconds (`1500`) or a Go duration (`1.5s`). `"1500"` in quotes is neither.

**Matching.** Most of a request is text: its path, query, headers and form values. A matcher reads that text as the type it compares with:
- `[equal, 10]` and `[isIn, [10, 20]]` match `?page=10`;
- `[gt, 18]` matches `?age=30`;
- `[equal, true]` matches `?debug=1`.

No other reading happens: `[equal, 10]` doesn't match `010`. The [match package docs](match/doc.go) state the rule.

**Keys.** Keys are kept as written, so a query name like `userId` matches `userId` only. Header names, and methods written as text, ignore case, so `method: get, post` matches `POST`; cookie names don't.

**URLs.** `url: /items?page=2` matches that path and query exactly: the same parameters, in any order, each with its values in order, so `/items` and `/items?page=2&sort=asc` don't match. Path segments compare decoded, so `%41` is `A`, but `%2F` isn't `/`. A relative `url` with a `*` in its path is an error: match a path with a glob through `path: /items/*`, and parameters one by one through `query`. For a literal `*`, write `%2A`. An absolute `url`, such as `https://api.example.com/items`, compares as text ignoring case, with the server's URL, or the one a request names in absolute form, as the forward proxy gets it, or intercepted, without its default port. In Go, `RequestURI("/items?page=2")` matches as a relative `url` does.

**Path templates.** `path_template: /users/{id}/orders/{orderId}` matches paths of its shape: as many segments, the literal ones the same once decoded, and a non-empty one for each parameter, so `/users/7/orders` and `/users/7/orders/9/` don't match. A parameter is a whole segment; write a literal brace as `%7B` or `%7D`. `path_params` matches the parameters by name, decoded, and templates read them as `.Request.PathParams.id`, or `index .Request.PathParams "order-id"` for a name that isn't a Go identifier. `url`, `url_match` or `path` can match the request too. In Go, `URLPathTemplate` and `PathParam`.

```yaml
request:
  method: GET
  path_template: /users/{id}
  path_params:
    id: [regex, '^[0-9]+$']
response:
  body: 'user {{ .Request.PathParams.id }}'
  template:
    enabled: true
```

**Many values.** `query`, `header` and `form` read the first value of a name. `queries`, `headers` and `forms` (`Queries`, `Headers` and `FormFields` in Go) read every value, as a list, so they take matcher lists, such as `[contains, a]`, `[itemsMatch, [a, b]]` or `[len, 2]`: they refuse text, which never equals a list. A name the request doesn't have is an empty list, which `[not, present]` matches, and a header sent empty isn't. A header line such as `a, b` is one value.

**More of the request.** `cookie` matches cookies by name, as `Cookie` does; a cookie the request doesn't have is null, which no text matches. `basic_auth: {username: jeff, password: s3cret}` matches Basic credentials, as `BasicAuth` does, and `BasicAuthMatches` takes matchers; without credentials, the username and password are null, which no text matches. `host` matches the host the request names, without its port, and text ignores case: `Host` in Go. `client_ip` matches the client's address, as the server's connection has it, not `X-Forwarded-For`: `ClientIP` in Go.

**JSON bodies.** `[eqj, {...}]` matches JSON equal to the document given, in full. An object of options after it loosens that: `ignore_array_order: true` pairs list items up in any order, and `ignore_extra_elements: true` ignores members and list items the stub doesn't name. `[jsonschema, schema]` matches JSON that conforms to a JSON Schema, an object or JSON text, of draft 2020-12 unless `$schema` or `{draft: 7}` says otherwise; it loads no `$ref` outside the schema. In Go: `EqualJSON(doc, IgnoreArrayOrder(), IgnoreExtraElements())` and `JSONSchema(text, SchemaDraft(JSONSchemaDraft7))`.

```yaml
request:
  method: POST
  path: /pets
  body: [all, [
    [eqj, {name: Rex, tags: [a, b]}, {ignore_array_order: true, ignore_extra_elements: true}],
    [jsonpath, $.id, [regex, '^[0-9]+$']]
  ]]
```

**XML bodies.** `[eqx, '<order id="7"><item>1</item></order>']` matches an equal XML document, by what it means: attributes in any order, text trimmed, comments and indentation left out, names compared by namespace URI. Options: `ignore_child_order: true`, and `namespaces: strict` to compare prefixes too, or `none` to read no namespaces. `[xpath, expression, matcher]` applies the matcher to what an XPath 1.0 expression selects: nothing is null, one result is itself, more are a list, so `[some, m]` matches any of them; an element is its XML. `{namespaces: {t: "urn:todo"}}` binds a prefix of the expression. In Go: `EqualXML`, `IgnoreChildOrder`, `XMLNamespaces`, `XPath` and `XPathNamespace`.

**Binary bodies.** `[equalbytes, "//4A"]` matches a body whose bytes are those of the standard base64 given, whose line breaks are ignored, exactly: no text reads as a number. In Go, `EqualBytes(b)`. `Equal(b)` matches the same bytes, but a mock's description, which the admin API serves, writes them as base64 text, which loads back as `[eq, "<base64>"]`, another matcher: `EqualBytes` describes them as `[equalbytes, ...]`, so the mock loads back.

**Dates and times.** `[before, x]`, `[after, x]` and `[equalDateTime, x]` (`eqdt`) compare a date and time in RFC 3339, in ISO 8601 without a zone, or in an HTTP format. `x` is one of those, or `now`, and either may end with an offset: `"now +3 days"`, `"2026-01-01T00:00:00Z -2 weeks"`. Options: `truncate_expected` and `truncate_actual`, each one of `first minute of hour`, `first hour of day`, `first day of month`, `first day of next month`, `last day of month`, `first day of year`, `first day of next year` and `last day of year`; `truncate_last: true` to offset first; `actual_format` with a Go layout, such as `02/01/2006`; `local_time: true` to compare wall clocks. An `x` without a zone is local, as with `local_time`; a request's date and time without a zone, against an `x` with one, is read in the server's zone. In Go: `Before`, `After`, `EqualDateTime`, `BeforeNow`, `AfterNow` and `EqualDateTimeNow`, with typed options such as `ExpectedOffset(3, match.Days)`.

```yaml
request:
  header:
    X-Date: [after, "now -1 hours"]
  query:
    until: [before, "now +15 days", {truncate_expected: first day of month}]
```

**Lists of matchers.** `[hasExactly, [[equal, "1"], [contains, "2"]]]` matches a list of as many values as matchers, each value matched by a matcher of its own, in any order. In Go, `ItemsMatch` takes matchers as items.

**Custom matchers.** A server reads mock files with its registry of matchers: the built-in ones, and the custom ones you give it. A custom matcher is built from its arguments when the mock loads, and its name works anywhere a matcher list does, in mock files, admin API mocks and request journal queries. `request.custom` lists matchers of the whole request: each gets the `*http.Request` (`Builder.Matches` in Go). A registry belongs to the servers you give it to; servers can share one, gRPC ones too, whose `request.custom` matchers get the `*mockgrpc.Call`.

```go
registry := matchbuild.MustNew(matchbuild.WithMatcher("body-too-long",
	func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
		if len(args) != 1 {
			return nil, errors.New("takes a length")
		}

		limit, ok := args[0].(int64)
		if !ok {
			return nil, errors.New("takes a length")
		}

		return match.Function(func(v any) (bool, error) {
			r, ok := v.(*http.Request)
			return ok && r.ContentLength > limit, nil
		}), nil
	}))

m := mockhttp.MustNew(mockhttp.Setup().Matchers(registry))
```

```yaml
request:
  custom: [[body-too-long, 2048]]
response:
  status: 413
```

**Proxy replies.** A `proxy` reply (`From` in Go) answers with what its target answers: see [Proxying](#proxying).

**Templates.** A mock file is plain JSON or YAML. Only the settings that are templates, such as a templated body, render: see [Templates](#templates).

**Errors.** A file with problems isn't loaded. The error lists every problem with its key and a fix:

```
mock: 2 problems
  file testdata/vlatte/_mocks/users_mock.yaml: unknown_key: response.stauts is not a known setting; did you mean response.status?
  file testdata/vlatte/_mocks/users_mock.yaml: conflict: proxy is set with response; keep one of them
```

## Templates

Replies can render parts of the response from the request: header values, the body, and the name or content of a body file. So can multipart parts and webhooks.

```yaml
request:
  method: POST
  path: /users

response:
  status: 201
  header_template:
    location: '/users/{{ .Request.Header.Get "X-User-Id" }}'
  body: '{"id": {{ uuid | toJSON }}, "name": {{ (fromJSON .Request.Body.String).name | toJSON }}}'
  template:
    enabled: true
```

In Go:

```go
m.MustMock(mockhttp.Postf("/users").Reply(mockhttp.Created().
	HeaderTemplate("Location", `/users/{{ .Request.Header.Get "X-User-Id" }}`).
	BodyTemplate(`{"id": {{ uuid | toJSON }}}`)))
```

**What is a template.** Nothing else renders: `{{` anywhere else is text.
- `header_template` values (`HeaderTemplate`).
- With `template.enabled`: a text `body` (`BodyTemplate`), the content of a `body_file` (`BodyFileWithTemplate`), the text of `multipart` parts (`Template()`), and the text of `event_stream` events (`Template()`).
- `body_file` names, always. The content of the file is written as it is unless `template.enabled` is set.
- Webhook parameters: `url_template`, `header_template`, `body_template` and `multipart_template`.

**Data.** Templates read:

| Field | What it holds |
|---|---|
| `.Request.Method`, `.Request.URL`, `.Request.URLPathSegments`, `.Request.Header`, `.Request.ID` | The request. `index .Request.URLPathSegments 0` is the first segment of the path. |
| `.Request.PathParams.<name>` | The parameters of the mock's [path template](#mock-files), decoded. |
| `.Request.Body.String`, `.Request.Body.Content`, `.Request.HasBody` | The body. JSON bodies are text: read their fields with `fromJSON`. |
| `.Request.Cookie "name"`, `.Request.PostForm.Get "f"`, `.Request.FormFile "f"` | Cookies and forms. |
| `.Server.URL` | The server's base URL. |
| `.Parameters.Get "key"`, `.Parameters.Has "key"` | The server's parameters. |
| `.Data` | The map set with `m.SetData`. |
| `.Provided.<key>` | What `Setup().TemplateDataProviders(...)` provide. |
| `.Extra` | The reply's `template.data`, or `SetTemplateData` in Go. Webhooks don't have it. |

**Functions.** Besides those of Go templates, such as `if`, `range`, `eq`, `len`, `index`, `slice`, `printf`, `html` and `urlquery`, templates call the functions below. The value a function works on comes last, so it pipes: `{{ .Request.Header.Get "X-Name" | trimSpace | toUpper }}`. List functions take the list first, as Go's `append` does. A bad argument, such as an unknown algorithm or a division by zero, fails the template.

| Kind | Functions |
|---|---|
| JSON | `toJSON v`, `fromJSON text`, `JSONPath path v` (the first value a JSONPath selects in a value or JSON text, or nil), `JSONPathAll path v`, `deleteJSONPath path v`, `mergePatch patch v` ([RFC 7386](https://www.rfc-editor.org/rfc/rfc7386)); each `v` and `patch` is a value or JSON text, so a string is JSON text; `indentJSON text`, `compactJSON text` |
| Strings | `toUpper`, `toLower`, `trimSpace`, `title`, `upperFirst`, `truncate max s`, `replaceAll old new s`, `split sep s`, `join sep list`, `substring start end s` (end -1 is the end), `slug`; they count runes, not bytes |
| Regexps | `regexpFind pattern s`, `regexpSubmatch pattern s`, `regexpReplaceAll pattern repl s` (`$1` is a group), `regexpMatch pattern s`, in Go's syntax |
| Values | `contains item collection` (a substring, a list item or a map key), `default fallback v` (when v is nil, `""` or empty; 0 and false are values) |
| Numbers | `add`, `sub`, `mul`, `div`, `mod` (ints when both operands are integers, as in Go, so `div 7 2` is 3; float64 otherwise), `toInt v` and `toFloat v`, which convert a numeric string, such as a query parameter: arithmetic takes numbers only |
| Lists | `list items...`, `append list item`, `insert list index item`, `remove list index` (-1 is the last), `randomItem list`, `shuffle list`, `reverse list`, `sortBy list path` (by what a JSONPath selects in each item, `"$"` for plain values; stable, missing values first). They return new lists. |
| XML | `xpath expr xml` (the text of what an XPath 1.0 expression selects, or the number or boolean it computes), `xpathNodes expr xml` (each node's `.Name`, `.Text` and `.Attrs`; printing an element writes its XML) |
| Time | `now`, `formatTime layout t` and `parseTime layout s` (a Go layout, `RFC3339` and the other names of package `time`, `unix` or `unixMilli`), `timeAdd offset t` (`"3 days"`, `"-1 month"` or a Go duration such as `"1h30m"`), `inZone zone t`, `truncateTime point t` (`"first day of month"`, `"last day of year"`, ...) |
| Encoding | `base64Encode`, `base64Decode`, `queryUnescape`, `parseQuery text` (form text as `url.Values`: `(parseQuery $s).Get "a"`) |
| Random | `uuid`, `randomString kind length` (`alphanumeric`, `alphabetic`, `numeric`, `alphanumericSymbols` or `hex`), `randomInt min max` and `randomFloat min max` (in [min, max)). Not for secrets. |
| Hashes | `hash alg encoding s` (`md5`, `sha1`, `sha224`, `sha256`, `sha384`, `sha512`, `sha3-224` to `sha3-512`; `hex` or `base64`), `hmac alg key encoding s` (`sha1`, `sha256` or `sha512`), as webhook providers sign payloads: `{{ hmac "sha256" "secret" "hex" .Request.Body.String }}` |
| Environment | `env name`: see below |

```yaml
response:
  body: |
    {"id": {{ JSONPath "$.order.id" .Request.Body.String | toJSON }},
     "expires": {{ now | timeAdd "30 days" | formatTime "RFC3339" | toJSON }},
     "items": {{ JSONPathAll "$.order.items[*].sku" .Request.Body.String | len }}}
  template:
    enabled: true
```

**Environment variables.** `env` reads only the variables the server's settings name, by exact name: `template.env` (`--template-env API_URL,TENANT`, `VLATTE_TEMPLATE_ENV`, or `Setup().TemplateEnv(...)` in Go) and `grpc.template.env` for gRPC mocks. Without it, templates read none, so a mock file can't print the server's secrets. A mock whose template names a variable that isn't allowed fails as it loads; a name read from the request is checked when it renders. An allowed variable that isn't set is `""`: `{{ env "TENANT" | default "acme" }}`. On an engine of your own, allow them with `vlatte.WithEnv`.

Add your own functions to the engine:

```go
engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{"upper": strings.ToUpper}))
m := mockhttp.MustNew(mockhttp.Setup().TemplateEngine(engine))
```

**Missing keys** are errors. `{{ .Extra.nmae }}` fails the request and names the key, instead of writing `<no value>`, and so does `.Parameters.Get` of a parameter that isn't set. Read an optional key with `index`, as in `{{ with index .Extra "nick" }}{{ . }}{{ end }}`, and an optional parameter with `.Parameters.Has` first. `vlatte.WithMissingKey` changes the rule for map keys.

**Escaping.** Templates are Go's `text/template`, so a body can be any format and nothing is escaped for you. Write JSON values with `toJSON`, and use `html` or `urlquery` where they fit.

**Errors.** Templates compile when the mock is built, so one that doesn't compile fails the mock. A body file whose content is a template compiles when a request reads it, since its name can depend on the request. A template that fails at a request answers as a [failure](#failures): the error status, `VLatte-Error: reply`, and a body that names the mock and the error.

**File names.** A body file name rendered from the request stays inside the directory written before the template: `files/{{ .Request.URL.Query.Get "f" }}` opens files in `files` only, and refuses `../` or an absolute path from the request.

**Header values** are written without the spaces and line breaks around them, as `net/http` writes headers, so a YAML block scalar works. A line break inside a rendered value fails the reply, or the webhook, so the request can't add a header.

**Webhooks** compile their templates when the mock is built, and render them each time they run, with the data of the request that completed the mock.

**Other engines.** A server compiles all its templates with one `vlatte.TemplateEngine`. vlatte ships `vlatte.GoTemplateEngine`; another engine, such as one for Handlebars, plugs in by implementing two methods:

```go
type TemplateEngine interface {
	Compile(name, text string) (Template, error)
}

type Template interface {
	Execute(w io.Writer, data any) error // safe for concurrent use
}
```

Pass it with `Setup().TemplateEngine(e)`. Templates then use its syntax everywhere, mock files included, as in `{{Request.Method}}` for Handlebars. [`template_handlebars_test.go`](mockhttp/e2e/template_handlebars_test.go) wraps [raymond](https://github.com/mailgun/raymond) in a few lines. Other engines read the same fields, maps and methods without arguments; methods with arguments, such as `.Parameters.Get "key"`, are Go template syntax.

## Forms and multipart

Go's `net/http` parses request bodies: `ParseForm` reads urlencoded forms and `ParseMultipartForm` reads `multipart/form-data`. Bodies are parsed by their media type, in any case: `text/plain` and `application/json` as text, and the two form types as forms.

**Fields.** `FormField` (file key `form`) reads a field of either encoding, as `http.Request.PostFormValue` does: its first value, and never the query. `FormFields` (file key `forms`) reads every value.

**Files.** `FormFile` (file key `form_file`) matches the files of a field with a pattern. Some file of the field must meet every condition of the pattern, or every file with `Every()` (`every: true`). With several patterns for one field, each must be met by some file of the field, and one file may meet several.

```go
m.MustMock(mockhttp.Postf("/upload").
	FormField("title", match.Equal("Holiday")).
	FormFile("avatar", mockhttp.FileWith().
		Filename(match.HasSuffix(".png")).
		ContentType(match.Equal("image/png"))).
	Reply(mockhttp.Created()))
```

```yaml
request:
  method: POST
  path: /upload
  form:
    title: Holiday
  form_file:
    avatar:
      filename: [hasSuffix, .png]
      content_type: image/png   # the media type, in lower case, without parameters
      body_file: avatar.png     # the file's bytes; or body: text or a matcher list
```

Go's rules apply:
- a part without a field name isn't read;
- a file's name is its base: `dir/a.png` arrives as `a.png`;
- a text field keeps no headers;
- a form holds at most 1000 parts, which `GODEBUG=multipartmaxparts` changes.

A form body that doesn't parse, or that was cut short, has no fields at all, and the server logs a warning. Other multipart requests, such as `multipart/mixed`, aren't parsed: match them with `Body`.

**Multipart replies.** `Reply.Multipart` writes a body of any multipart subtype with Go's `multipart.Writer`. The body sets the `Content-Type`, with its boundary. When none is set, a random boundary is drawn once per mock, so every response is the same bytes.

```go
m.MustMock(mockhttp.Getf("/report").Reply(mockhttp.OK().Multipart(mockhttp.NewMultipart("related",
	mockhttp.NewPart().Header("Content-Id", "<root>").BodyJSON(map[string]any{"id": 1}),
	mockhttp.NewPart().Name("report").Filename("report.pdf").BodyFile("testdata/report.pdf"),
).Param("start", "<root>"))))
```

```yaml
response:
  status: 200
  multipart:
    type: related              # the subtype: form-data, mixed, related, x-mixed-replace…
    params: {start: "<root>"}
    parts:
      - header: {content-id: "<root>"}
        body: {id: 1}          # text as is, anything else as JSON
      - name: report
        filename: report.pdf
        body_file: report.pdf  # relative to the mock file
```

- **Bodies.** A part takes one of `body`, `body_base64` and `body_file` at most. A body that isn't UTF-8 text, such as an image, goes in `body_base64`, as standard base64 (`body_base64: //4A`), whose line breaks are ignored, or in a `body_file`. A byte body set in Go with `Part.Body` describes in `body_base64` when it isn't UTF-8, or when the body is templated, so it loads back as bytes, not as a template.
- **Defaults.** A part with a name or a filename gets its `Content-Disposition`. A JSON body defaults to `application/json`, and a part with a filename to `application/octet-stream`.
- **Templates.** With `template.enabled`, or `Template()` in Go, the text of the parts is a template: names, filenames, content types, header values, text bodies and body file names. Byte bodies, such as `body_base64`, JSON bodies and the content of body files are written as they are.
- **Encode.** `Multipart.Encode` writes the same bodies for requests in tests.

**Streaming.** A part with a `delay` waits that long before it is written, and everything before it is flushed first. A client therefore reads each part as it comes, as `multipart/x-mixed-replace` camera feeds need.

```yaml
response:
  status: 200
  multipart:
    type: x-mixed-replace
    parts:
      - {content_type: image/jpeg, body_file: frame1.jpg}
      - {content_type: image/jpeg, body_file: frame2.jpg, delay: 500ms}
```

**Webhooks** send multipart bodies too:
- the `multipart` parameter takes the same object, and `multipart_template` makes its text a template;
- Go uses `webhook.Options().Multipart(mockhttp.MultipartForm(...))`;
- a webhook sends its body at once, so its parts can't have delays;
- relative body files open from the mock file's directory, as in replies: a whole `body_file` and a part's alike.

**Templates** read forms: `{{ .Request.PostForm.Get "title" }}` and `{{ with .Request.FormFile "avatar" }}{{ .Filename }}{{ end }}`.

**Recording** matches a recorded upload by all its text fields, each with its values in order, and its files by `form_file` patterns, so it replays whatever boundary the next client picks. Files that aren't UTF-8 text go to files next to the recording. An upload with another field doesn't match; one with more files still does. See [Recording and playback](#recording-and-playback).

## Server-Sent Events

`Reply.EventStream` serves a `text/event-stream` body event by event, each flushed to the client as it goes, over HTTP/1.1 and HTTP/2. It sets `Content-Type: text/event-stream` and `Cache-Control: no-cache`, unless the reply sets them: a `Content-Type` header can add a charset. A HEAD request gets those headers only. A status without a body, 1xx, 204 or 304, fails the mock.

```go
m.MustMock(mockhttp.Getf("/prices").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Name("price").ID("1").DataJSON(map[string]any{"value": 10}),
	mockhttp.NewEvent().Name("price").ID("2").DataJSON(map[string]any{"value": 11}).Delay(time.Second),
).Retry(3*time.Second).Heartbeat(15*time.Second).KeepOpen().ResumeFromLastEventID())))
```

```yaml
response:
  status: 200
  event_stream:
    retry: 3s           # sent first: how long clients wait before they reconnect
    event_delay: 100ms  # before each event without a delay of its own
    heartbeat: 15s      # a comment each time the stream waits this long
    keep_open: true     # stay open after the last event, until the client leaves
    resume: true        # a Last-Event-ID gets only the events after that id
    events:
      - {event: price, id: "1", data: {value: 10}}  # text as is, anything else as JSON
      - {event: price, id: "2", data: {value: 11}, delay: 1s}
      - {comment: "keep-alive"}
      - {data: "line 1\nline 2"}                   # a data line per line
      - {data_json: "done"}                        # always JSON, never a template: sends "done", quotes too
      - {raw: "data: x\r\n\r\n"}                   # sent as it is
```

- **Fields.** `event`, `id`, `retry`, `comment` and `data` or `data_json` go on the wire as the [WHATWG event stream format](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation) reads them. Data and comments with line breaks get a line each, so clients read them back whole. An `event` or `id` with a line break, or an `id` with a NUL, fails the mock, or, in a templated stream, the reply that renders it. An empty `id` is sent: it resets the client's last event ID.
- **Raw.** `raw` (`Raw` in Go) sends its text as it is, in place of the fields but `delay`, to test a client's parser: CRLF or CR line endings, a byte order mark, an event without its blank line. An `id` line in `raw` is sent, but `resume` doesn't know it. A byte order mark counts only as the first bytes of the stream, so not after a `retry` or a heartbeat.
- **Delays.** An event's `delay` wins over `event_delay`. Everything before a wait is flushed first. In Go, `EventDelayFunc` takes a `vlatte.Delay`, such as `vlatte.Jitter`; a description leaves it out.
- **Ending.** A stream ends after its last event, and clients reconnect after `retry`. With `keep_open`, it ends when the client leaves or the server closes. The mock counts the request once the stream starts, so `times` and scenarios see an open stream, and runs its callbacks and `on_complete` events once it ends, with the body it sent.
- **Reconnecting.** `resume` sends a request with a `Last-Event-ID` header only the events after the one with that id; an unknown id gets them all. Match the header to answer reconnections with another mock, and serve `204 No Content`, a reply without `event_stream`, to tell clients to stop reconnecting.
- **Templates.** With `template.enabled`, or `Template()` in Go, event types, ids, comments and text data are templates. JSON data, `data_json` (`DataJSON` in Go) and `raw` are sent as they are. A templated stream's description keeps JSON data as JSON: built back, it is sent compact, with its object keys sorted. JSON with a number past int64, which a file can't hold, is written as its text, a template then.
- **Faults.** [Steps and faults](#faults) apply to the stream as to any body: `fault.LimitData` drops the connection after some bytes, as a flaky network does.
- **Mappers.** Response mappers read the whole body, which a stream may never end, so a mock can't have both.

## Webhooks

A webhook is a request a mock sends after it replies, as a payment API calls back to say an order was paid. The `webhook` package sends them, as an `on_complete` listener of the server.

**Registering.** A mock names its listener, so the server registers one under that name first. The `vlatte` command registers a webhook as `webhook`. In Go:

```go
wh, err := webhook.New()
m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener("webhook", wh))
```

A mock that names a listener the server doesn't have fails to build, and so does a server given two listeners of one name.

A webhook sends through `webhook.WithTransport`, or else the server's `Setup().Transport`, or else a transport the server makes for webhooks, whose idle connections `m.Close()` closes; a gRPC server makes one too. A listener of your own gets the same from its event's `Kit()`: the server's `Logger()`, `Transport()`, and `Dir()`, the directory of the mock file, which relative files open from.

```yaml
request:
  method: POST
  path: /orders

response:
  status: 202

on_complete:
  - name: webhook
    parameters:
      method: POST
      url_template: '{{ .Data.callback }}/orders/{{ (fromJSON .Request.Body.String).id }}'
      header:
        content-type: application/json
      body: '{"status": "paid"}'
      delay: 500ms
```

In Go:

```go
m.MustMock(mockhttp.Postf("/orders").
	OnCompleteEvent(webhook.Options().
		URL(callback).
		Method(http.MethodPost).
		Header("Content-Type", "application/json").
		Body(`{"status": "paid"}`).
		Delay(500 * time.Millisecond).
		Build("webhook")).
	Reply(mockhttp.Accepted()))
```

`URLTemplate`, `HeaderTemplate`, `BodyTemplate` and `BodyFile` set the parameters of the same names. A multipart body whose text is templates is `Multipart(mockhttp.MultipartForm(...).Template())`.

**Parameters.**

| Parameter | What it sets |
|---|---|
| `url` or `url_template` | Where the request goes: an absolute `http` or `https` URL. One of them is required. |
| `method` | The method, in any case. Empty means `GET`. |
| `header`, `header_template` | Headers by name, each a string, or a list of strings to send the header once per value, in order. Names ignore case, so a name is set once. |
| `body`, `body_base64`, `body_template`, `body_file`, `multipart`, `multipart_template` | The body, one of them at most. A body that isn't UTF-8 text goes in `body_base64`, as standard base64, whose line breaks are ignored; a `Body` set in Go that isn't UTF-8 describes there. Multipart bodies are as in [replies](#forms-and-multipart). |
| `delay` | How long to wait before sending, once the mock replied. The response waits for it too, unless the webhook runs after the response. |
| `timeout` | How long the request may take, from sending it to its answer. Without it, the listener's timeout, or else 10s. |

Templates render with the data of the request that completed the mock: see [Templates](#templates). A mistake, such as a missing URL, two bodies or a template that doesn't compile, fails the mock when it is built.

**When they run.** Webhooks are sent once the mock has replied, each on its own, and the response ends when they all have: a slow or failing webhook holds up no other, and a test checks what they sent right after the call:

```go
// callbacks is a mock server standing for the client the webhook calls back.
res, err := http.Post(m.URL("/orders"), "application/json", strings.NewReader(`{"id": 42}`))
// ...
callbacks.AssertCalled(t)
```

A webhook's `delay` counts from the reply, so it holds the response too. A reply that streams its parts, or a body over a few KB, sends its status and headers before its webhooks are done, so the call returns early; the body still ends after them, unless the mock sets `Content-Length` itself. Read the body to its end, as `io.ReadAll` does, before checking what they sent. `m.Close()` cancels the webhooks still waiting, for their delay or for their target.

**After the response.** `after_response: true` on an `on_complete` entry, beside `name` and `parameters`, sends the response without waiting for the event, for an app that needs its callback once it has its answer. In Go, set `AfterResponse` on the `vlatte.EventConfig` that `Build` returns. The event starts as the response goes out, so give it a `delay` when the client must read its answer first, and have the test wait for the target to get the webhook, not for the call. `m.Close()` cancels these events too, and waits for them. A failure is logged as any other, but the journal entry of the request, recorded by then, doesn't tell it.

```yaml
on_complete:
  - name: webhook
    after_response: true
    parameters:
      url: http://localhost:8081/orders
      delay: 100ms
```

**Failures.** A webhook fails when its target doesn't answer in time, or answers with a status outside 2xx: a redirect isn't followed. The server logs the failure as a warning, and the request's [journal](#request-journal) entry, if the server keeps one, tells it in its `Message`. The timeout is 10 seconds: a webhook's `timeout` parameter (`Options().Timeout(d)` in Go) changes it for that webhook, and `webhook.WithTimeout` for every webhook its listener sends, where 0 means 10 seconds, unlike `proxy.timeout`. The CLI's `webhook.timeout` setting is the `WithTimeout` of its listener, for HTTP and gRPC mocks. A webhook reads up to 256 KiB of the answer, so its connection carries the next one. A response waits for a webhook at most its delay plus the timeout.

**Logs.** Webhooks log each request and its answer with the server's logger, so `Setup().Silent()` silences them too. `webhook.WithLogger` gives them a logger of their own.

## Proxying

vlatte sends requests on to real servers in three ways:

- a **proxy reply**: a mock that answers with what its target answers, `From(target)` in Go and the `proxy` key in mock files;
- the **forward proxy**: clients use the server as their HTTP proxy, `Setup().Proxy()` in Go and the `proxy` setting, and what no mock matches goes to its host;
- the **recording target**: see [Recording and playback](#recording-and-playback).

The three share one engine, and answer alike:

- The body streams as the upstream sends it, trailers included. A body of unknown length, or an event stream, reaches the client as it comes.
- The hop-by-hop headers of either connection, such as `Connection`, `Keep-Alive` and `Proxy-Authorization`, don't go on. A request's `Te: trailers` does.
- The upstream's `VLatte-*` headers, such as the debug headers of an upstream that is a vlatte server too, don't reach the client: it gets the proxy's.
- Compressed bodies go on as they came, and a redirect goes back to the client: the proxy doesn't follow it.
- A request that asks to switch protocols, such as a WebSocket handshake (`Connection: Upgrade`, `Upgrade: websocket`), asks the upstream too. Once the upstream switches, the client gets its `101`, and the two connections are piped until both sides end, or the server closes, while the mock's callbacks and events run. A mock counts the call, and the log shows it, when the switch is done. A mock's steps apply to the connection too: see [Steps](#steps). A mock with response mappers doesn't ask the upstream to switch, as they can't apply to a connection: the upstream answers as to any request. `h2c`, which asks for HTTP/2 on the client's own connection, isn't asked on. An upstream that switches to a protocol the client didn't offer fails the call. A switch needs HTTP/1.1.
- The timeout bounds the wait for the response headers, connecting and sending the request included: 30 seconds by default, and 0 waits forever. The body has no time limit.
- A call that fails gets the server's answer to a failure, `ErrorStatusCode` with `VLatte-Error: reply`, unless the client left: then the log warns. Errors and logs show the upstream's URL without its password, and with its query values masked: `?api_key=xxxxx`. An upstream body that breaks off cuts the client's connection, so the client doesn't take part of it for all of it.
- A request the proxy sends on carries the server's ID in `VLatte-Via`. A request that passes the same server 10 times, as in a loop, such as through a proxy reply whose target is the server itself, fails as a proxy loop rather than being sent on forever. A route may pass a server more than once, such as through a gateway that sends it back.
- What the proxy keeps of a body, for the log, callbacks and events, is at most 1 MiB, and only when one of them reads it.
- A request body the server read whole, shorter than `MaxBodyParsingLimit` and without `NoBodyParsing`, can be sent again: the transport resends a replayable request, such as one with an `Idempotency-Key`, on a new connection when the one it reused turned out closed.

**Proxy replies.** The target's path and query come before the request's: with `target: http://api.local/v1?tenant=a`, a request to `/users?page=2` goes to `http://api.local/v1/users?tenant=a&page=2`. `trim_prefix` and `trim_suffix` cut the request path first: `trim_prefix` cuts whole segments, so `/api` cuts `/api/users` to `/users` and leaves `/apiary` as it is, and `trim_suffix` cuts text, such as an extension: `.json` cuts `/users.json` to `/users`. When the target has a path, a request path with a `.` or `..` segment, escaped or not, fails: it could leave that path. `request_header` sets headers of the request the target gets, in place of the client's values of the same name, and `remove_request_headers` leaves request headers out; `header` sets headers of the response, in place of the target's. `x_forwarded: true` (`XForwarded()` in Go) tells the target about the client: its address in `X-Forwarded-For`, after those of the proxies before, the host it asked for in `X-Forwarded-Host`, and its scheme in `X-Forwarded-Proto`. `preserve_host: true` (`PreserveHost()`) sends the target the `Host` the client asked for, in place of the target's own. `timeout` is the timeout. A mock's `delay` comes before the call: a client that leaves during it gets nothing, and the target isn't called. A mock with response mappers reads the whole body first, so they see it in `Body`: up to 1 MiB, the most a proxy keeps of a body. A larger body fails the reply, and so does an event stream (`text/event-stream`) at once, as it doesn't end; a stream of another type that never ends waits for the client to leave. A mock's steps can change the body's length: the target's `Content-Length` doesn't go on. A mock's description, which the admin API serves, keeps the target as it was set, credentials included, so it loads back as the same mock.

The target's TLS certificate isn't verified unless `ssl_verify: true`, so a target with a self-signed certificate answers. Set it for a target that gets credentials, such as `https://user:pass@api.local`, whose user info goes as Basic auth: over `http`, it goes in cleartext. `ssl_verify` also decides whether the certificate of an `https` proxy on the way, from `HTTPS_PROXY`, is verified.

**Proxy all.** `ProxyAll(From(target))` in Go mocks what no other mock matches with a proxy reply: it matches any method and path, and comes last, at priority `math.MaxInt`. It leaves alone `CONNECT`s, and, on a server that is a forward proxy, the requests in absolute form, which the proxy sends on to their hosts. In a mock file, `proxy_all` takes the keys of `proxy` and makes the same mock, without `request`; a `ProxyAll` mock's description loads back as one. The `proxyall` recipe mocks it. The `proxy_all` setting does the same without a mock, so assertions don't count it, and `Clean` and `Reload` leave it: `vlatte --proxy-all-target=https://api.example.com`. It takes the keys of a mock file's `proxy` but `request_header` and `header`, which flags and variables can't give.

**Forward proxy.** A client sends its requests to the server in absolute form, `GET http://api.local/users`, and opens a tunnel with `CONNECT` for an `https` URL. A mock that matches the request answers it; mocks match the path and ignore the host, unless they match it, with `host` or an absolute `url`. A request no mock matches goes to its host. A request in origin form, `GET /users`, names no host: no mock matching it, it gets the no-match answer. `proxy.timeout` is the timeout, and `proxy.ssl_verify` verifies the certificates of the hosts, and of an `https` proxy on the way, `proxy.via` or the one of `HTTPS_PROXY`: none is verified by default. `proxy.via` sends the requests through another proxy, an `http`, `https`, `socks5` or `socks5h` URL whose user info goes as its credentials: a client's `Proxy-Authorization` is for vlatte, and doesn't go on.

A `CONNECT` no mock matches opens a tunnel to its host, through `proxy.via` or the proxy of the environment, if any, within `proxy.timeout`, and the log shows that it opened. What goes through a tunnel, such as a TLS connection, isn't read: mocks don't see it, unless the proxy intercepts it. Each side of a tunnel ends what it sends on its own, and the tunnel closes once both did, or once the server closes. A tunnel needs HTTP/1.1, and a response writer that can hijack its connection: a middleware's writer must unwrap, with `Unwrap() http.ResponseWriter`, to the server's.

**HTTPS interception.** With `proxy.intercept` (`ProxyConfig{Intercept: true}` in Go), the proxy serves the TLS of a `CONNECT` no mock matches itself. It answers with a certificate for the host the `CONNECT` names, or the name the client asks for, signed by its CA, which clients must trust. The requests within are then the server's own, for `https://` that host, which `scheme` and an absolute `url` see: mocks answer them, recordings record them, the log shows them, and the proxy sends the rest on to the host. A connection that doesn't start with TLS goes to the host through a tunnel, as does one whose client offers only `h2` by ALPN, such as a gRPC client, and one whose client sends nothing for 2 seconds, waiting for a host that speaks first, such as an SSH server: a host the proxy can't reach then closes the connection, as the `200` went out already. `proxy.ca_cert` and `proxy.ca_key` name the CA's files, which the settings need: a CA's certificate, or an intermediate CA's, followed by its chain, which the proxy sends with each host's. In Go, `ProxyConfig.CA` sets it, and without one, the server makes a CA of its own, valid for a year, which `m.ProxyCA()` returns for clients to trust:

```go
m := mockhttp.MustNew(mockhttp.Setup().Proxy(&mockhttp.ProxyConfig{Intercept: true}))
pool := x509.NewCertPool()
pool.AddCert(m.ProxyCA())
client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}}
```

An intercepted connection speaks HTTP/1.1.

**Transports.** In Go, `From(target).Transport(rt)` sends a proxy reply through a transport of yours, such as one with a client certificate, and `Setup().Transport(rt)` sets one for every proxy reply, the recording target, the forward proxy and webhooks. A transport of yours replaces the one vlatte makes, and decides on its own how it verifies certificates and which proxy it goes through: `ssl_verify`, `proxy.via`, `proxy.ssl_verify` and `record.skip_ssl_verify` fail with it, rather than being ignored. The timeouts apply to any transport.

The transports vlatte makes go through the proxy of `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, which leaves out `localhost`, unless `proxy.via` sets one. Docker can set these variables in containers from `~/.docker/config.json`, and so send the hosts of a compose network, such as `http://api:8080`, to that proxy: add them to `NO_PROXY`. A variable that names the vlatte server itself sends the requests it proxies back to it, until they fail as a proxy loop: set `NO_PROXY=*` for its process, or `proxy.via`.

## Steps

In Go, `Step` passes the body of each answer of a mock through steps, in order, before it reaches the client: to delay it, send it in parts, change it or cut it. A step is a `vlatte.Step`. For each answer, its `Open` makes the step's stage of that answer's `vlatte.Flow`, so a stage keeps what it counts for one answer only: a mock's answers don't share it.

```go
upper := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
	if _, err := next.Write(bytes.ToUpper(b)); err != nil {
		return 0, err
	}

	return len(b), nil
})

m.MustMock(mockhttp.Getf("/hello").Step(upper).Reply(mockhttp.OK().PlainText("hello")))
```

A stage gets each write of the body, each flush, and the end of the answer, an empty body included. It can pass a write on as it is, changed, in parts, later or not at all, and send what it holds at a flush or at the end. It may write from a goroutine of its own, and needs no lock: vlatte orders its writes with the reply's. Steps can change the body's length, so the answer has no `Content-Length`. A stage aborts the answer by returning an error that matches `vlatte.ErrAbort`, or with `Flow.Abort`, such as from a timer, which ends a reply that waits on the answer's context, as a proxy reply does. The mock still counts the call and runs its callbacks and events. Then the connection is cut, after what was sent: an HTTP/1 connection is closed, and an HTTP/2 stream is reset. With `vlatte.ErrReset`, an HTTP/1 connection is reset too, so the client reads `ECONNRESET`. A failure, such as a failing reply, is vlatte's answer, and skips the steps.

`WireStep` passes every byte of each answer through its steps, after those of `Step`: the status line, the headers, the framing of the body and the body, so a step can send what isn't HTTP, such as nothing at all, half the headers, or garbage after a valid head. The mock takes its connection over and answers in HTTP/1.x itself, with `Connection: close`: the body is chunked over HTTP/1.1, and ends with the connection over HTTP/1.0. A request over HTTP/2 can't give its connection over, so it gets vlatte's error answer: turn HTTP/2 off, or use an HTTP/1.1 client, to test a mock with wire steps. An abort closes the connection without the rest of the answer, and `vlatte.ErrReset` resets it, once the mock counted the call and ran its callbacks and events. A recording leaves out the answers of mocks with wire steps.

`UpstreamStep` passes what a mock sends on through its steps: the body of the request a proxy reply sends to its target. Steps can change its length, so the target gets it chunked. The server reads the body of a request before it matches a mock, so these steps don't slow the client's upload: they apply to what the mock sends on. A body the server doesn't read whole, with `NoBodyParsing` or past `MaxBodyParsingLimit`, is read from the client as the target reads it, a small piece at a time, so a step that slows it slows the upload too, once the connection's buffers are full. A mock with wire steps takes its connection over before it replies, so it reads such a body through these steps first: they pace the upload, and a proxy's target gets the body as they passed it on, at once. A stage that aborts ends the call to the target, and the client's answer is cut as for any abort.

A connection that switched protocols, such as a WebSocket, goes through the steps too. The answer ends with its head, so its steps end there, and a stage that cuts it leaves the client without a switch. Then what the target sends the client passes the mock's steps again, in a flow of its own, then its wire steps, which saw the `101` too. What the client sends the target passes its upstream steps. A stage that aborts either way, even on a timer while nothing comes, cuts both sides, or resets them with `vlatte.ErrReset`.

## Faults

The `fault` package has steps that make a mock answer wrong, to test how a client handles a server that fails, or a bad network. A step of either level acts on the body with `Step`, and on every byte of the answer, its head included, with `WireStep`:

| Step | Level | What the client gets |
|---|---|---|
| `fault.EmptyResponse()` | `WireStep` | No byte at all: the connection ends where it waits for a status line. |
| `fault.MalformedResponseChunk()` | `WireStep` | The status line and headers, with the mock's status, then garbage in place of the first chunk of the body, then the end of the connection. |
| `fault.RandomDataThenClose()` | `WireStep` | `fault.RandomDataSize` random bytes in place of the answer, then the end of the connection. |
| `fault.ConnectionResetByPeer()` | `WireStep` | A TCP reset, such as `ECONNRESET`, before a status line. |
| `fault.ChunkedDribbleDelay(chunks, total)` | `Step` | The whole body, in `chunks` flushed parts spread over `total`: part k comes at k·total/chunks, and the body ends at `total`. The body is held until the reply wrote all of it. |
| `fault.Latency(latency, jitter)` | Either | Every write and flush `latency` later, give or take `jitter` at random, in order. The answer keeps its pace: the writer doesn't wait. |
| `fault.Bandwidth(bytesPerSecond)` | Either | Each write at that rate, in flushed pieces of a tenth of a second. `fault.Bandwidth3G` and the other `fault.Bandwidth*` constants are common links, in bytes per second. |
| `fault.Slicer(averageSize, sizeVariation, delay)` | Either | Each write in flushed slices of `averageSize` bytes, give or take `sizeVariation` at random, `delay` apart. |
| `fault.SlowClose(delay)` | Either | The whole answer, then its end `delay` later: the body's last chunk with `Step`, the close of the connection with `WireStep`. |
| `fault.Timeout(timeout)` | Either | Nothing, then the end of the connection `timeout` after the answer began; with `Step`, the head comes with that end. A timeout of 0 never ends. |
| `fault.LimitData(n)` | Either | The first `n` bytes, then the end of the connection: bytes of the body with `Step`, of the whole answer with `WireStep`. |
| `fault.ConnectionResetByPeerAfter(d)` | Either | The answer as it comes, then a TCP reset `d` after it began. With `Step` over HTTP/2, the stream is cut instead. |

```go
m.MustMock(mockhttp.Getf("/users").WireStep(fault.ConnectionResetByPeer()).Reply(mockhttp.OK()))
m.MustMock(mockhttp.Getf("/report").Step(fault.ChunkedDribbleDelay(5, 2*time.Second)).Reply(mockhttp.OK().BodyFile("report.csv")))
m.MustMock(mockhttp.Getf("/search").WireStep(fault.Latency(300*time.Millisecond, 60*time.Millisecond)).Reply(mockhttp.OK()))
m.MustMock(mockhttp.Getf("/video").Step(fault.Bandwidth(fault.Bandwidth3G)).Reply(mockhttp.OK().BodyFile("video.mp4")))
```

A wire fault needs HTTP/1.x, as wire steps do. Faults combine with other steps: a fault on the wire comes after the steps of `Step`. A mock that answers with a fault still counts the call, and runs its callbacks and events, before the connection ends.

**In mock files,** `steps`, `wire_steps` and `upstream_steps` list faults for `Step`, `WireStep` and `UpstreamStep`. A fault is an object: `type` names it, after its function in snake case, and its other keys are its parameters, by name. Durations are whole milliseconds or a Go duration, such as `1.5s`. A parameter with a default can be left out.

| `type` | Parameters |
|---|---|
| `empty_response`, `malformed_response_chunk`, `random_data_then_close`, `connection_reset_by_peer` | None. |
| `chunked_dribble_delay` | `chunks`, `total` |
| `latency` | `latency`; `jitter`, 0 by default |
| `bandwidth` | `rate`: bytes per second, or a `fault.Bandwidth*` constant in snake case: `gprs`, `dial_up`, `2g`, `good_2g`, `3g`, `good_3g`, `dsl`, `4g`, `wifi` or `fiber` |
| `slicer` | `average_size`; `size_variation` and `delay`, 0 by default |
| `slow_close` | `delay` |
| `timeout` | `after`, 0 by default: never |
| `limit_data` | `bytes` |
| `connection_reset_by_peer_after` | `after` |

```yaml
request:
  method: GET
  path: /report
response:
  status: 200
  body_file: report.csv
steps:
  - type: bandwidth
    rate: 3g
  - type: connection_reset_by_peer_after
    after: 5s
wire_steps:
  - type: latency
    latency: 300ms
    jitter: 60ms
```

A fault with a missing parameter, a key it doesn't take, or a value its function refuses fails the file, with each problem at its key, such as `steps[1].after`. `faultbuild.Build`, of package `fault/faultbuild`, builds a fault from its object in Go.

A mock describes its faults the same way, so the admin API shows them and its store keeps them. A step of your own, such as a `vlatte.StepFunc`, isn't in the description.

## Recording and playback

A server records what it proxies as mock files, then answers the same requests the same way, without the service.

```go
m := mockhttp.MustNew()
m.MustStart()
t.Cleanup(m.Close)

err := m.StartRecording(
	mockhttp.RecordDir("testdata/recorded"),
	mockhttp.RecordTarget("https://api.example.com"),
	mockhttp.RecordPaths("/v1/*"),
)
// ... requests to m.URL("/v1/...") reach the target, and are recorded ...

res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

**What is recorded.**
- With a target, the requests the recording takes go to the target before any mock, and its answers are recorded. The others go to the mocks, as usual.
- Without one, the recording takes what the server proxies: the forward proxy (`Setup().Proxy()`) and proxy replies (`From`, the `proxy` key). A mock's own answer is recorded only with `RecordIncludeMocked(true)`.
- Only an answer the client got whole is recorded. The failures vlatte answers, such as a target it can't reach, aren't.

**Filters.** `RecordMethods`, in any case, and `RecordPaths` choose the requests the recording takes. A path is a glob whose `*` matches `/` too, so `/v1/*` takes `/v1/a/b`. `RecordFilter` takes any request pattern, made as a mock is:

```go
mockhttp.RecordFilter(mockhttp.Request().Header("X-Record", match.Equal("yes")))
```

A filter that fails takes nothing: the request is served as usual, and the failure is logged.

**Playback.**
- `StopRecording` waits until every call it took is written, then serves the recordings: the mock files load again, with the recording's directory, so the hits of file mocks count from 0. When `ctx` ends first, it returns ctx's error, and the recording keeps writing: another `StopRecording` waits for it again, and a `StartRecording` once it wrote everything ends it. Either serves it. A `StopRecording` that `Close` cuts short says the recordings aren't served.
- A server started later serves them too. The default mock file patterns include `testdata/vlatte/_mocks_recorded`, and a server serves the recordings of its `record.dir`.
- A server serves the recordings of the last recording `StopRecording` ended, not those of earlier ones in other directories. The same request recorded into two directories a server serves has one ID twice: `StopRecording` reports it, and the server serves what it served before.
- A mock that proxies every request needs a priority number above 0 to let the recordings answer first: `AnyMethod().Priority(10).Reply(From(target))`.
- A recording answers its request before any proxy, so a later recording without a target doesn't record that request again: it records what is new. A recording with a target records every request it takes.

**What a recording matches.** A recording answers the request it recorded, and no other:

| Part | Matched |
|---|---|
| Method | Ignoring case. |
| Path and query | As `url` matches: `url: /items?page=2`. |
| Headers | Those `RecordRequestHeaders` names, by their first value, or their absence. A redacted one by its presence. No other header. |
| Body | A JSON body as JSON, whatever its spacing and member order. A urlencoded or multipart form by all its text fields. Other text as it is, and no body as none. The media type of the `Content-Type` too. |
| Files of a form | By name, content type and content, as `form_file` patterns. |

Calls a recording can't tell apart are one mock, whose answers play in turn, then the last one again. The same answer every time is one. `RecordRepeats(vlatte.RecordFirst)` keeps the first answer only.

A recording doesn't match what it can't read, and then takes any body: one that isn't UTF-8 text, or is larger than `MaxInlineBody`, or that `MaxBodyParsingLimit` cut, or that a parser of the server's config read, or the urlencoded form of a request other than POST, PUT and PATCH, which Go doesn't read. The forward proxy's host isn't recorded: one path on two hosts is one mock.

**What it answers.** The status, the headers but those the server sets (`Date`, `Content-Length` and the hop-by-hop ones) and `Set-Cookie`, the trailers, and the body as the client got it, after the steps of the mock.
- A text body up to `MaxInlineBody`, 1 MiB by default, is written in the mock file. Other bodies go to files next to it, named after their content, so equal bodies share a file. `RecordBodyFiles(true)` writes every body to a file.
- A gzip body that fits in `MaxInlineBody` decoded is recorded decoded, with `encoding: gzip`, and compressed again when it is served, into bytes that may differ. So a gzip body with a header or trailer that holds for its bytes, a strong `ETag`, `Content-MD5`, `Digest`, `Content-Digest`, `Repr-Digest` or `Content-Range`, is kept as sent, as are other encoded bodies, with their `Content-Encoding`.
- A larger body goes to a file as it streams, so it never sits in memory whole. When the disk is slower than the sender, the answer waits for it, a second in all at most. Past that, or past `MaxBodySize`, 1 GiB by default, the call isn't recorded, and the client still gets every byte.
- A body file plays back streamed, without a `Content-Length`.
- `RecordExcludeResponseHeaders` sets the headers left out, in place of `Set-Cookie`: name it among them to keep leaving it out, or name none to keep every header. `RecordTransform` changes each recording, as Go values, before it is written:

```go
mockhttp.RecordTransform(func(r *mockhttp.RecordedMock) error {
	for i := range r.Responses {
		delete(r.Responses[i].Header, "X-Request-Id")
	}
	return nil
})
```

**Secrets.** Mock files are plain text, and often committed: review them first. They hold the request bodies recordings match, such as a password or a `client_secret`, the values of the headers `RecordRequestHeaders` names, the response headers and bodies, such as tokens, and the query, in the URL and in the file name. `Set-Cookie` is left out by default. The headers `Setup().RedactHeader(...)` names keep their values out too: a response header is written `<redacted>`, and a request header matches by its presence. `RecordTransform` hides the rest.

**Files.** A recording writes a mock file named after its request, such as `GET-items-page-2-<hash>.mock.json`, or `.yaml` or `.yml` with `RecordFormat`, into its directory: `{workdir}/_mocks_recorded` by default, `testdata/vlatte/_mocks_recorded`. A mock file appears whole, or not at all.

**Status.** `RecordingStatus` counts the calls taken, the mocks written, and the calls dropped or that failed. A response never waits for the calls before it to be written: when too many wait, the next ones are dropped and counted.

**Settings.** `Setup().Record(...)` records from start to close, and so does the `record` section of the config:

```yaml
# .vlatterc.yaml
record:
  target: https://api.example.com
  paths: ["/v1/*"]
  request_headers: [Accept]
  exclude_response_headers: [Set-Cookie, X-Request-Id]
```

The target's TLS certificate is verified: a recording sends the target its requests, credentials included. `record.skip_ssl_verify`, or `RecordSkipSSLVerify(true)`, skips it, such as for a self-signed certificate. In Go, `RecordTransport(rt)` sends through a transport of yours instead, such as one that trusts the target's CA, and `skip_ssl_verify` doesn't apply to it. `vlatte --help` lists every `record` setting.

**Admin API.** The [admin API](#admin-api) starts and stops recordings while the server runs: `POST /recording` takes the keys of the `record` section, `GET /recording` counts the calls, and `DELETE /recording` stops it and lists the files it wrote. Its `/recorder` page does the same from a browser.

## Admin API

The admin API manages a server while it runs. It is an HTTP server of its own, on its own address, the same for mock servers of every protocol, and it leaves every path of the mock server to the mocks. It serves only when asked, from the `admin` package:

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew(admin.Addr("127.0.0.1:8081")))) // no Addr picks a random local port
m.MustStart()
fmt.Println(m.AdminURL()) // http://127.0.0.1:8081
```

A gRPC server serves the same admin API with `mockgrpc.Setup().Admin(admin.MustNew(...))`, with gRPC mock files, and recordings with the keys of its `grpc.record` section. An `Admin` serves one mock server. A server that doesn't ask for an admin API links neither it nor what it serves with.

`admin.Use(middlewares...)` wraps every HTTP request of the admin API, `POST /mcp` included, in the middlewares given, the first outermost, such as one that checks an authentication header and answers `401` in place of the admin API. MCP over stdio doesn't go through them.

The `vlatte` command serves the admin API by default: `admin: false` (`--admin=false`, `VLATTE_ADMIN=false`) turns it off, and `admin.addr` (`--admin-addr`, `VLATTE_ADMIN_ADDR`) sets its address. Its gRPC server serves an admin API of its own, which `grpc.admin: false` (`--grpc-admin=false`) turns off and `grpc.admin.addr` (`--grpc-admin-addr`) places. These settings are the command's: a server made in Go ignores them, even in a `.vlatterc` it shares with the command, and serves an admin API only when its code asks for one.

| Route | What it does |
|---|---|
| `GET /health` | Answers once the admin API serves, with the kind of the server and the URL of its mocks. |
| `GET /livez` | The Kubernetes liveness probe: answers as `/health` does, whatever the mock store says. |
| `GET /readyz` | The Kubernetes readiness probe: answers as `/health` does once the mock store's `Health` returns nil, and `503` while it returns an error. |
| `GET /startupz` | The Kubernetes startup probe: answers as `/health` does. The admin API serves only once the mock server loaded its mocks. |
| `POST /reset` | Removes the mocks added through the admin API, loads the mock files again, saved ones included, starts every scenario over, and clears the [journal](#request-journal). Mocks added in code stay as they are. The mocks of a persistent store come back from it: reset never deletes what it holds. |
| `GET /mocks` | Lists every mock, enabled or not, with its `priority`, `enabled`, `hits` and `origin`. |
| `POST /mocks` | Adds the mock of the body, a [mock file](#mock-files). An id in use is a `409`. |
| `GET /mocks/{id}` | The mock as a mock file describes it, which `PUT` and `POST` take back. A mock whose reply a mock file can't hold, an `Echo`, `Func` or `Handler`, answers `500` with the code `describe_failed`. |
| `PUT /mocks/{id}` | Replaces the mock, in one step. The body's id must be the path's. |
| `PATCH /mocks/{id}` | Enables or disables the mock: `{"enabled": false}`. |
| `DELETE /mocks/{id}`, `DELETE /mocks` | Removes the mock, or every mock. |
| `POST /mocks/validate` | Checks a mock as `POST /mocks` would, without adding it. |
| `GET /schema` | The [JSON Schema](mockhttp/schema/http.schema.json) of mock files, for clients that write them. A gRPC server answers `501`. |
| `POST /mocks/reload` | Loads the mock files, and the [persistent store](#persistent-admin-mocks), again, as `m.Reload()` does. A mock file's mock takes the place of a mock added through the admin API with its id. |
| `POST /mocks/save` | Writes the mocks added through the admin API as mock files, `<id>.mock.<format>`, and serves them from the files from then on. `{"dir": ..., "format": ...}` defaults to `{workdir}/_mocks` and YAML, which the default mock file patterns load on the next start; `dir` stays inside `root_dir`. When a mock can't be written as a mock file that loads back, nothing is written, and files that fail to load are put back as they were. |
| `GET /scenarios`, `PUT /scenarios/{name}`, `DELETE /scenarios` | Lists the state of each scenario, moves one to a state, `{"state": "paid"}`, and starts every scenario over. Names ignore case: `Checkout` is `checkout`. |
| `GET /requests` | Lists the newest requests of the [journal](#request-journal), with their `count`: `?limit=` (100 by default, 1000 at most, `0` for the count alone) and `?before=`, the `next` of a page, page through them. |
| `QUERY /requests` | Finds and counts the requests a query selects, in the body: see [Request journal](#request-journal). It uses the HTTP [`QUERY`](https://httpwg.org/http-extensions/draft-ietf-httpbis-safe-method-w-body.html) method, safe as `GET` is, with a body: `curl -X QUERY`. |
| `GET /requests/{id}`, `DELETE /requests` | Gets a request by the id its answer's `VLatte-Request-Id` names, and clears the journal. |
| `GET /events` | Streams what the server serves as it happens, as [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html): requests a mock answered, requests no mock matched, proxied requests, warnings and errors. Nothing is kept: the [journal](#request-journal) keeps the requests. `curl -N <admin>/events` follows it. |
| `GET /logs` | Streams the server's log lines, as it writes them, as Server-Sent Events. A server given its own `Logger` streams none. |
| `POST /recording`, `GET /recording`, `DELETE /recording` | Starts a [recording](#recording-and-playback) with the keys of the `record` section, counts its calls, and stops it, listing the files it wrote. |
| `GET /recorder` | A page that records a service from a browser. |
| `POST /mcp` | The [MCP](#mcp) endpoint, when the admin API serves MCP; `501` otherwise. |
| `GET /`, `GET /docs`, `GET /openapi` | A page that renders the OpenAPI document, at the root and at `/docs`, and the document. The page loads [Scalar](https://scalar.com) from a CDN, so it needs internet: the version vlatte pins, checked against its hash, or a newer one that `admin.DocsVersion("1.80.0")` (`admin.docs_version`, `--admin-docs-version` for the command) names. |

**Formats.** Bodies are JSON, or YAML with `Content-Type: application/yaml`. Answers follow `Accept`: JSON, or YAML when it is preferred. A relative `body_file` in a mock sent to the admin API opens from the server's working directory, as `GET /mocks/{id}` writes it.

**Errors** are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem documents with a stable `code`; the problems of a mock are listed one by one:

```json
{
  "status": 400,
  "title": "Bad Request",
  "code": "invalid_body",
  "detail": "the mock has 1 problem",
  "instance": "/mocks",
  "problems": [
    {
      "code": "unknown_key",
      "origin": "admin request",
      "key": "response.stauts",
      "message": "response.stauts is not a known setting",
      "hint": "did you mean response.status?"
    }
  ]
}
```

A request net/http refuses before the admin API reads it, such as one with a malformed request line, is answered in plain text.

**Safety.** The admin API has no authentication: anyone who reaches it can change the mocks, which can read files the server can, and read the requests of the journal. It listens on a local address unless told otherwise; with `VLATTE_DOCKER_HOST`, the CLI listens on every interface, as for the mocks, so publish its port only where the network is trusted. A browser's cross-site request that would change the server, or query its journal, is refused with `403`.

### MCP

With `admin.MCP()`, the admin API serves its operations to AI agents too, over the [Model Context Protocol](https://modelcontextprotocol.io): a tool per operation, named after it, such as `add_mock`, `list_mocks` and `query_requests`; the streams, the OpenAPI document and the pages have none. A tool's arguments carry what its route takes, the body under a key, such as `{"mock": ...}` for `add_mock` and `{"query": ...}` for `query_requests`, and its tool's schema names them. A tool answers what its route answers, or its problem document as the tool's error. An agent sees only the tools the server can do: a gRPC server has no `add_mock`, and one without a journal no `query_requests`.

```go
api := admin.MustNew(admin.MCP())
m := mockhttp.MustNew(mockhttp.Setup().Admin(api).LogOutput(os.Stderr)) // over stdio, only MCP writes to stdout
m.MustStart()
fmt.Println(m.AdminURL() + "/mcp") // Streamable HTTP, for an agent that runs elsewhere
err := api.ServeMCP(ctx, os.Stdin, os.Stdout) // stdio, for an agent that starts the program, until stdin ends
```

`POST /mcp` keeps no sessions and answers in JSON. It is as open as the rest of the admin API: see Safety above.

The `vlatte` command serves MCP at `POST /mcp` by default, and `vlatte mcp` serves it on stdin and stdout, for an agent that starts the command as its MCP server. `vlatte mcp` takes the server's flags, and writes its banner and logs to stderr. `mcp: false` (`--mcp=false`, `VLATTE_MCP=false`) turns MCP off. An agent's MCP settings start it as any command:

```json
{"mcpServers": {"vlatte": {"command": "vlatte", "args": ["mcp", "--mock-files=mocks/*.yaml"]}}}
```

### Persistent admin mocks

The mocks the admin API adds last as long as the server, unless a persistent store keeps them. Each change the admin API makes, adding, replacing, enabling, disabling or removing a mock, reaches the store at once, and the server serves what the store holds when it starts and on each reload, so a change made to the store directly takes effect then too. The mocks of mock files and code never reach it, and a mock file's mock wins over a mock of the store with its id.

`FileStore(dir)` keeps them as mock files, `<id>.mock.yaml`, which only the store loads: keep `dir` out of the mock file patterns. A relative `dir` starts at `root_dir`, and `{workdir}` at the [work directory](#work-directory). A mock whose description doesn't load back is refused, as its file would fail every later start. The `vlatte` command keeps them in `{workdir}/_admin`: `admin.dir` (`--admin-dir`, `VLATTE_ADMIN_DIR`) sets another directory, and an empty one keeps the mocks in memory. In Go, `AdminStore` needs `Admin`:

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew()).AdminStore(mockhttp.FileStore("{workdir}/_admin")))
```

Any `vlatte.Store[*mockhttp.Mock]` can be the store, such as a database collection. It keeps the descriptions of the mocks by ID: `EncodeMock` writes one, and `DecodeMock` builds the mock back for the server. Its `Health(ctx)` returns an error while it can't serve the mocks, such as when the database is down: `GET /readyz` answers `503` until it returns nil.

```go
mockhttp.Setup().AdminStore(func(app *mockhttp.Server) (vlatte.Store[*mockhttp.Mock], error) {
	return &collectionStore{app: app, coll: coll}, nil // Store: EncodeMock(mock, "json"); PickAll: DecodeMock(app, doc, "json")
})
```

`POST /mocks/save` is apart from the store: it exports the admin API's mocks as mock files to commit, which then serve in place of the store's copies.

## Request journal

A server can keep a journal of the requests its mocks get, whatever their outcome: a mock answered it (`matched`), none matched (`unmatched`), another server did (`proxied`), serving it failed (`failed`), or its client left first (`abandoned`). A test then verifies the requests it sent, while matching and hits work as before. Each entry has the status the client got: the one a reply that writes its own answer sent, as `Handler` does, `200` when it sent none, and for a failure the status sent before it, else `error_status_code`, as for a panic. A middleware (`Use`) or the decorator (`HandlerDecorator`) that answers before the mock, in its place, or once a timeout ends it, sets that status, as it does in the logs and the admin API's events.

```go
m := mockhttp.MustNew(mockhttp.Setup().Journal("")) // "" keeps it in a temporary directory, which Close removes
m.MustStart()
t.Cleanup(m.Close)

// ... the code under test sends its requests ...
// journal is github.com/caffeine-projects/vlatte/journal.

m.AssertRequested(ctx, t, 1, journal.Query{Method: match.Equal("POST"), Path: match.Equal("/orders")})
m.AssertRequested(ctx, t, 2, journal.Query{Header: map[string]vlatte.Matcher{"Authorization": match.HasPrefix("Bearer ")}})
m.AssertNotRequested(ctx, t, journal.Query{Outcome: journal.OutcomeUnmatched})

// The request part of a mock verifies as a mock matches: query parameters, forms, files, the parsed body.
m.AssertRequested(ctx, t, 1, journal.Query{}, mockhttp.Request().Method("POST").Query("page", match.Equal("2")))

entries, err := m.Requests(ctx, journal.Query{Path: match.GlobMatch("/users/*"), Limit: 10}) // newest first
n, err := m.CountRequests(ctx, journal.Query{Kind: mockhttp.Kind})
```

An entry's `Message` tells what failed: the request, or else the on_complete events of a request a mock answered, one line each, such as a [webhook](#webhooks) whose target answered `500`, so a test checks a webhook failed without reading the logs. A failed assertion lists the requests that matched, or the newest ones when none did, with the mock that came closest to each request no mock matched. A request is journaled as the server ends its answer, as hits count: a client gets a small answer only then, while it may read the start of a large one a moment before.

A `journal.Query`, of package `github.com/caffeine-projects/vlatte/journal`, selects by the fields every protocol has. `Kind`, `Outcome`, `MockID`, `Method`, `Path` and `Status` are indexed: a query runs each of their matchers once per value the journal holds, not once per request, and reads no request from disk for them. `Target` (the full URL), `Since` and `Until` filter in memory too. `Header`, `Body` and request patterns read the requests the other fields leave. `Limit` and `Before` page through them, newest first; `m.Journal().Find` returns the next page's cursor and the count. A `journal.Journal` works on its own too, as a changelog of any requests: `journal.New(journal.Options{Dir: dir})`.

Patterns are the request part of a mock builder, such as `mockhttp.Request()`: the server rebuilds each request from the journal and matches it, through its body parsers. A pattern has no `Times`, `Once` or scenario, which count served requests rather than match one, and no `ClientIP`: a rebuilt request has no `RemoteAddr`, nor TLS state for a `RequestMatches` predicate.

gRPC servers keep the same journal with `mockgrpc.Setup().Journal(dir)`: a call's path is its full method, such as `/pkg.Service/Method`, its headers its metadata, its body its request message as JSON, which `match.Field` and `match.JSONPath` read, and its status its code, the one the client got, after every interceptor of `ServerOptions`. Its answer's `vlatte-request-id` header names its entry. A client or bidi stream's body is the JSON array of the messages its client sent. Patterns are gRPC mock builders, such as `mockgrpc.Method("Greetings").Field("message", match.Equal("hi"))`, and the admin API's `request` key takes the request part of a gRPC mock file.
- With a journal, a stream interceptor goes through `grpc.ChainStreamInterceptor`: the journal takes `grpc.StreamInterceptor`, which grpc runs before every other, and `New` fails with `mockgrpc.ErrStreamInterceptorSlot` when `ServerOptions` sets it. A unary interceptor goes either way.
- A call an interceptor answers itself isn't journaled, nor one grpc answers before any interceptor, such as one whose request it can't decode. A call of a method no descriptor describes is journaled as unmatched.
- A unary reply grpc fails to send once every interceptor returned, such as one larger than a `grpc.MaxSendMsgSize` set, keeps the code they returned.
- A service registered on `GRPCServer()`, past the server, has its unary calls journaled within the interceptors, with the code its mocks answered with, which `Start` warns of: register services with `RegisterService`, or `pb.RegisterGreeterServer(m, ...)`.

**Files.** The journal is a changelog: each request is a JSON line, appended to `journal-NNNNNN.log` files. A new file starts every quarter of `journal.max_entries` requests (10 000 at most) or 64 MiB, so dropping the oldest requests removes whole files. Each full file, and the last one on a clean stop, gets a `.idx` sidecar with its index, so a restart reads no log. A text body stays text; another is `body_base64`. `DELETE /requests` and `Clear` write a `journal.clear` tombstone before they delete the files, so a restart drops a file a crash or a failed delete left. Only the user reads the files.

**Persistence.** A named directory keeps the requests across restarts: a server that starts again finds the requests it served before, and a test verifies against them too, so tests use `Journal("")`. One server at a time keeps a journal in a directory: another fails to start. An HTTP server's relative directory starts at `root_dir`, and `{workdir}` at its [work directory](#work-directory), as in `{workdir}/_journal`. Close closes the journal: verify the requests before it, as `t.Cleanup(m.Close)` leaves a test to.

**Limits.**
- The journal keeps the newest 10 000 requests (`journal.max_entries`, `0` for all), and 1 MiB of each body (`journal.max_body_size`, `0` for whole bodies).
- A body the journal cut, or that the server didn't read, has `body_truncated`: the server reads no body of a `GET` or `HEAD`, nor under `no_body_parsing`, nor past `MaxBodyParsingLimit`. A request pattern that reads the body, such as `Body`, `FormFile` or `RequestMatches`, which gets the whole request, fails on such a request, when the rest of the pattern matches it, rather than miss it: `AssertNotRequested` can't pass for a request the server got.
- The headers the server redacts in its logs (`RedactHeader`) are redacted in the journal. gRPC metadata is kept as it came.
- Requests the server answers before its mocks get them, such as CORS preflights, or that a middleware answers, aren't journaled.
- A named directory gets a `.gitignore` of its files, so committing `testdata` leaves the journal out.

The settings `journal` (`--journal`, `VLATTE_JOURNAL`), `journal.dir`, `journal.max_entries` and `journal.max_body_size` configure it.

Through the [admin API](#admin-api), `QUERY /requests` takes a query in the body, with the matchers of mock files, and answers the requests, newest first, and their `count`. `"limit": 0` answers the count alone. `request` is the request part of a mock file:

```sh
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"method": "POST", "path": "/orders", "request": {"query": {"page": "2"}}, "limit": 0}' \
  http://127.0.0.1:8081/requests
# {"count": 1, "requests": []}
```

| Key | Selects |
|---|---|
| `kind`, `outcome` | The protocol, such as `http`, and the outcome, exactly. |
| `mock_id`, `method`, `path`, `target`, `status`, `body` | By a matcher: text matches equal text, a number or a bool an equal value, and a list is a matcher, such as `["glob", "/users/*"]` or `["jsonpath", "$.id", ["equal", 7]]`. |
| `header` | Headers, or metadata, by name in any case, each by a matcher. |
| `since`, `until` | The time of the requests, in RFC 3339. |
| `request` | The request part of a mock file of the server's protocol. HTTP only. Send `method` and `path` on their own too: the index narrows the requests before `request` reads them. |
| `limit`, `before` | The page: 100 requests by default, 1000 at most, and the `next` of the page before. |

## Failures

When no mock matches, or serving a request fails, the server answers itself. It sends `418 I'm a teapot`, a status a real server rarely sends, so the answer never looks like a mock's, and the `VLatte-Error` header says why:

| `VLatte-Error` | Why |
|---|---|
| `no-match` | No mock matched the request. |
| `match` | Finding a mock failed: a matcher, or the store, returned an error. |
| `reply` | A mock matched, and its reply failed: a template, a body file, a template data provider or a response mapper. |
| `panic` | Serving the request panicked. |

The body says the same in text, and names the mock and the error. A test checks the header:

```go
require.Equal(t, vlatte.FailureReply, res.Header.Get(vlatte.HeaderFailure))
```

The status of `no-match` is `no_match_status_code`, and the status of the others is `error_status_code`. Both default to 418.

The answer to a request no mock matched lists the mocks that came closest, as does its [journal](#request-journal) entry: the parts of the request each matched, those that differ, and those it didn't check, such as a scenario's state, which a report never runs. A `times` used up is a part a mock differs in.

```
REQUEST DID NOT MATCH
GET http://127.0.0.1:8080/users

CLOSEST MOCKS
[users] list users: 2 of 3 parts matched; differs in header Accept
[create]: 1 of 2 parts matched; differs in method
```

The report runs no matcher twice: it goes on from the part where matching stopped, which a mock differs in, so it runs only the matchers matching never reached, the action of a `match.Peek` among them. It runs none that keeps state, such as a scenario's, which it lists as not checked. A mock that wants the request but is used up, by its `Times`, differs in `times` only, and comes first. `near_misses: false`, `--near-misses=false` or `mockhttp.Setup().NoNearMisses()` turns it off.

A gRPC server answers `NotFound` when no mock matches (`Setup().NoMatchCode` sets another), `Unimplemented` for a method no descriptor describes, and `Internal` when serving a call fails, with the same values in the `vlatte-error` trailer. A mock that answers with a status, as `Status(...)` does, gets no trailer: that status is its answer.

## Debug headers

Each answer says how the server served it, in `VLatte-*` headers:

| Header | Value |
|---|---|
| `VLatte-Matched-Mock-Id` | The ID of the mock that matched. |
| `VLatte-Matched-Mock-Name` | Its name, when it has one. |
| `VLatte-Matched-Mock-Hits` | Its hits, this request included. |
| `VLatte-Matched-Mock-Source` | The file it was loaded from, when it was loaded from one. |
| `VLatte-Request-Id` | The ID of the request: `.Request.ID` in templates, `request_id` in the logs, its id in the [journal](#request-journal). Every answer has it, failures and no-match included. |
| `VLatte-Proxied` | `true` when a real service answered instead of a mock: a mock that replies with `From(...)`, a proxy passing on a request no mock matches, or the target of a recording. |

A failure keeps the headers of the mock that matched before it failed. Hits count once the answer is sent, so two requests served at the same time can show the same number. A test checks them like any header:

```go
require.Equal(t, "users-get", res.Header.Get(vlatte.HeaderMatchedMockID))
```

Header names don't depend on case: Go sends `Vlatte-Matched-Mock-Id`, and HTTP/2 sends them in lower case. Recordings leave the debug headers out.

`debug_headers: false` turns them all off, as do `VLATTE_DEBUG_HEADERS=false`, `--debug-headers=false` and `mockhttp.Setup().NoDebugHeaders()`. `VLatte-Error` stays: it is not a debug header.

A gRPC server sends `vlatte-matched-mock-id`, `vlatte-matched-mock-name` and `vlatte-matched-mock-hits` as header metadata, with a mock's message or status, and `vlatte-request-id` when it keeps a journal. `Name(...)` on a mock builder sets the name. `mockgrpc.Setup().NoDebugHeaders()` turns them off.

## Configuration

A server's settings come from these sources, each overriding the ones before it:

1. Defaults.
2. Code: the options passed to `mockhttp.New`, such as `mockhttp.Setup().Addr(":8080")`.
3. A `.vlatterc.json`, `.vlatterc.yaml` or `.vlatterc.yml` file in the working directory or in `testdata`. `--config` or `VLATTE_CONFIG` names another file.
4. `VLATTE_*` environment variables.
5. Command-line flags, for the `vlatte` command.

Each setting has a key that its variable and flag derive from: `log.level` is `log: {level: debug}` in a file, `VLATTE_LOG_LEVEL=debug` and `--log-level=debug`. `vlatte --help` lists every setting.

In a file, each setting has its type, as in mock files: `addr: ":8080"` in quotes, `max_age: 150` bare, and a list as a list. Environment variables and flags are text:
- bools read as `strconv.ParseBool` reads them;
- numbers are written in the JSON number grammar;
- lists are comma-separated.

`log.level` and `log.verbosity` take a name, in any case, such as `info`.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
proxy:
  timeout: 1.5s # or milliseconds: 1500
cors: true
```

Setting a key of a section (`cors`, `proxy`, `proxy_all`, `record`, `admin` or `journal`) turns the section on, and `false` turns it off.

`root_dir` is the directory that relative mock file patterns and record directories start at. Absolute ones are used as they are.

### Work directory

A server keeps its own files in its work directory: `testdata/vlatte`, or `testdata/vlatte/<name>` for a server `name` names, so servers that share a directory keep apart. `workdir` (`--workdir`, `Setup().WorkDir(dir)`) sets another, as is, from `root_dir`. Without it, a server's name is one folder, which doesn't start with `_`, as vlatte's own do.

```
testdata/
  vlatte/
    _mocks/            mock files
    _mocks_recorded/   recordings
    _admin/            the mocks the CLI's admin API adds
    _journal/          the CLI's journal
    test/              a server named "test": its own _mocks, _admin, _journal...
```

A path setting that starts with `{workdir}`, such as `mock_files`, `record.dir`, `admin.dir` or `journal.dir`, starts at the work directory: the defaults do, as `--help` shows. Other relative paths start at `root_dir`. In a YAML file, quote such a value: `dir: "{workdir}/recordings"`.

Unknown keys, flags and `VLATTE_*` variables are errors, except `VLATTE_DOCKER_HOST`. A server that can't load its settings reports every problem at once:

```
server: error loading configuration sources
config: 2 problems
  env VLATTE_LOGLEVEL: unknown_key: VLATTE_LOGLEVEL is not a known setting; did you mean VLATTE_LOG_LEVEL?
  file .vlatterc.yaml: invalid_value: proxy.timeout must be milliseconds or a Go duration, not "10x"; such as 1500 or 1.5s
```

Servers in tests read the file and the environment too. Pick the sources with `ConfigSources`, or pass none to use code only:

```go
m := mockhttp.MustNew(mockhttp.Setup().ConfigSources())
m = mockhttp.MustNew(mockhttp.Setup().ConfigSources(mockhttp.ConfigFile("testdata/server.yaml")))
```

## CLI

`vlatte` serves the mock files under `testdata/vlatte/_mocks` in the working directory:

```sh
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/hello
```

Without `--addr`, it listens on a random port and prints the URL. Settings can also come from a `.vlatterc` file or `VLATTE_*` environment variables; see [Configuration](#configuration). Run `vlatte --help` to list them, and `vlatte completion --help` to set up shell completion.

Mock files can send [webhooks](#webhooks): the CLI registers the `webhook` listener, and `--webhook-timeout` bounds each request that sets no `timeout` of its own.

It [records](#recording-and-playback) a service until it stops, and serves the recordings on the next run:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded; another Ctrl-C quits at once
vlatte
```

It serves the [admin API](#admin-api) too, on an address of its own, which it prints: a random local port unless `--admin-addr` sets one, and `--admin=false` turns it off. With `VLATTE_DOCKER_HOST` set, it listens on port 8081 of that host, as the mocks do on 8080. `POST /mocks` adds a mock sent in the request body, for example as JSON:

```sh
vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/added"},"response":{"status":201}}' \
  http://127.0.0.1:8081/mocks
```

The command keeps the mocks its admin API adds in `{workdir}/_admin`, as [persistent admin mocks](#persistent-admin-mocks), so the next run serves them: `--admin-dir` moves them, and `--admin-dir=""` keeps them in memory.

It serves gRPC mocks too, when a setting of the `grpc` section is set, as `--grpc-*` flags, `VLATTE_GRPC_*` variables, or `grpc:` in `.vlatterc`. It listens on `:50051`, unless `--grpc-addr` says, serves the services of the descriptor sets `--grpc-descriptors` names, from the mock files `--grpc-mock-files` names, with server reflection, an admin API of its own, which it prints, and a journal of its calls:

```sh
protoc --descriptor_set_out=api.binpb --include_imports api.proto
vlatte --grpc-descriptors=api.binpb --grpc-mock-files='mocks/grpc/*.yaml'
grpcurl -plaintext -d '{"id": "u-1"}' localhost:50051 users.Users/Get
```

`--grpc-tls` serves them over TLS, with a self-signed certificate, unless `--grpc-tls-cert` and `--grpc-tls-key` name the files of one; `--grpc-tls-ca` requires clients to present a certificate signed by a CA of its file. Each of these turns TLS on.

It keeps a [journal](#request-journal) of the requests it serves in `{workdir}/_journal`, across restarts, which the admin API queries: `--journal-dir` moves it, `--journal-dir=""` keeps it in a temporary directory, and `--journal=false` turns it off. Two commands can't share a journal directory: give each a `--name`, which gives it a [work directory](#work-directory) of its own, `testdata/vlatte/<name>`. The journal holds the requests it got, secrets included: its directory ignores its files in git.

## Docker

The `Dockerfile` builds an image of the [CLI](#cli) for the platforms of its distroless base, such as `linux/amd64` and `linux/arm64`, run by a non-root user (`65532`):

```sh
make docker   # docker build -t vlatte:dev .
docker run --rm -p 8080:8080 -p 8081:8081 -v "$PWD/testdata/vlatte/_mocks:/vlatte/testdata/vlatte/_mocks:ro" vlatte:dev
```

The image sets `VLATTE_DOCKER_HOST`, so it listens on every interface, on fixed ports: mocks on `8080`, or `8443` with `--https`, the admin API on `8081`, and gRPC mocks on `50051`, with their admin API on `8082`. Its working directory is `/vlatte`, so the CLI's defaults apply below it: mock files in `/vlatte/testdata/vlatte/_mocks`, and a `.vlatterc` file in `/vlatte` or `/vlatte/testdata`. Arguments go to `vlatte`, and `VLATTE_*` variables set the same [settings](#configuration):

```sh
docker run --rm -p 8080:8080 -e VLATTE_LOG_LEVEL=debug vlatte:dev --proxy-all-target=https://api.example.com
docker run --rm vlatte:dev version
```

The CLI writes its journal and the mocks the admin API adds to `/vlatte/testdata/vlatte`, which the image's user owns. To keep them, or recordings, on the host, mount a `testdata` directory over `/vlatte/testdata` and run as its owner: `--user "$(id -u):$(id -g)" -v "$PWD/testdata:/vlatte/testdata"`. With a read-only root file system, give both of its writable paths a `tmpfs`:

```sh
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp --tmpfs /vlatte/testdata/vlatte:uid=65532,gid=65532 \
  -v "$PWD/testdata/vlatte/_mocks:/vlatte/testdata/vlatte/_mocks:ro" \
  -p 8080:8080 -p 8081:8081 vlatte:dev
```

Its `HEALTHCHECK` runs `vlatte health`, which asks the admin API's readiness probe, `GET /readyz` on `8081`, and exits with 0 when it answers 200. With the admin API off or on another address, it checks that the mock server accepts connections on `8080` or `8443` instead, so it needs no settings of its own. Kubernetes can probe `GET /livez` and `GET /readyz` with `httpGet` too.

## Development

```sh
make test   # run the tests with the race detector
make lint   # run golangci-lint
```

## License

[MIT](LICENSE)
