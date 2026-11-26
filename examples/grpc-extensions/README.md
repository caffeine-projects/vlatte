# Extend gRPC mocks

How can a mock match an ID inside a scanner's request and report that the
lookup completed? Open [extensions_test.go](extensions_test.go) for the
parser, custom reply and listener. Open
[parsed.mock.yaml](testdata/parsed.mock.yaml) for the field match, response
template and completion event.

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-extensions
```

The registered parser reads `scan:P1` as `parcel_id: P1`. Both mocks
match that field and return `found P1`. The Go `Func` reply can also read the
original request with `call.Message()`. In YAML, the template reads the parsed
field from `.Request.Message`. Both runs invoke the registered `audit`
listener once with the parameter `lookup`. The parser rejects `scan:` and
bare `P1` with `InvalidArgument("invalid scan id")`.

Implement the reply `Func`, register the request parser, and implement the
completion listener in Go. YAML can use the registered parser for matching
and templates and refer to the registered listener by name.
