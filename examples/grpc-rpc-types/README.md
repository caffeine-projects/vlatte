# Four gRPC call types with Go and YAML mocks

The parcel tracking service looks up a parcel, streams its progress, accepts a batch of scans, and exchanges live messages with a dispatcher. Each method has two standalone tests. The file-mock test loads YAML with `MockFiles`. The programmatic test registers the same behavior in Go with `MustMock`. Both use the generated client from [parcels.proto](pb/parcels.proto).

| Method | Client sends | Client receives | Go and file mocks |
|---|---|---|---|
| `GetParcel` · unary | One parcel ID and tenant metadata | One status with headers and trailers, or a gRPC error with a typed resource detail | [Go](unary_test.go) · [YAML](testdata/unary.mock.yaml) |
| `WatchParcel` · server streaming | One parcel ID | Ordered updates until EOF. A stream can also end empty or fail after an update. | [Go](serverstream_test.go) · [YAML](testdata/serverstream.mock.yaml) · [response data](testdata/route.json) |
| `UploadScans` · client streaming | A sequence of depot scans | One receipt when the client finishes sending. VLatte matches the complete sequence in order. | [Go](clientstream_test.go) · [YAML](testdata/clientstream.mock.yaml) |
| `Dispatch` · bidirectional streaming | Commands while the stream is open | Each command can produce several replies. Closing the send side triggers a final reply. | [Go](bidi_test.go) · [YAML](testdata/bidi.mock.yaml) |

From the repository root, run either server-streaming example on its own.

```sh
go -C examples test -v ./grpc-rpc-types -run '^TestWatchParcelFileMocks$'
go -C examples test -v ./grpc-rpc-types -run '^TestWatchParcelProgrammaticMocks$'
```

## Call the file mocks from a terminal

For this walkthrough, install `protoc` and `grpcurl`. From the repository root, compile the service descriptor and start VLatte. The descriptor tells the CLI which methods exist and which message type each one expects.

```sh
mkdir -p .local/grpc-examples
protoc -I examples/grpc-rpc-types/pb --include_imports \
  --descriptor_set_out=.local/grpc-examples/parcels.binpb parcels.proto

go run ./cmd/vlatte --addr=127.0.0.1:0 --admin-addr=127.0.0.1:0 \
  --workdir=.local/grpc-examples --grpc-addr=127.0.0.1:50051 \
  --grpc-descriptors=.local/grpc-examples/parcels.binpb \
  --grpc-mock-files='examples/grpc-rpc-types/testdata/*.mock.yaml'
```

Open a second terminal. Use reflection to list the four methods, then look up parcel `P1` as tenant `demo`. The unary reply has state `in_transit`. Try `P404` with the same tenant to see the application's `NotFound` error.

```sh
addr=127.0.0.1:50051
grpcurl -plaintext "$addr" list parcel.v1.Tracking
grpcurl -plaintext -H 'x-tenant: demo' -d '{"id":"P1"}' "$addr" parcel.v1.Tracking/GetParcel
```

Watch the same parcel. It sends `accepted`, `in_transit`, and `delivered` before the stream ends. Use `P0` for an empty stream or `P2` to see one update followed by `Unavailable`.

```sh
grpcurl -plaintext -d '{"id":"P1"}' "$addr" parcel.v1.Tracking/WatchParcel
```

Upload two scans. When the pipe reaches EOF, the client closes its send side and receives one receipt with `accepted` equal to 2. Reverse the scans to get `InvalidArgument`.

```sh
printf '%s\n' '{"site":"north"}' '{"site":"south"}' |
  grpcurl -plaintext -d @ "$addr" parcel.v1.Tracking/UploadScans
```

Open an interactive dispatch stream.

```sh
grpcurl -plaintext -d @ "$addr" parcel.v1.Tracking/Dispatch
```

1. Type `{"text":"route"}` and press Enter. Both `north` and `south` arrive while you can still send commands.
2. Type `{"text":"?"}` and press Enter to receive `unknown`.
3. Press Ctrl-D on an empty line. The server sends `bye` and the RPC completes.

To see dispatch reject a command, make a fresh call with a single input. It exits with `FailedPrecondition` and the message `dispatch closed`.

```sh
grpcurl -plaintext -d '{"text":"stop"}' "$addr" parcel.v1.Tracking/Dispatch
```

Stop the server with Ctrl-C in its terminal.
