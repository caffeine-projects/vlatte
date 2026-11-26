# gRPC reply strategies

How can a mock make a lookup fail once, pick a depot reproducibly, or echo
each dispatch message? Open [replies_test.go](replies_test.go) and
[replies.mock.yaml](testdata/vlatte/_mocks_grpc/replies.mock.yaml).

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-replies
```

- `GetParcel(retry)` returns `Unavailable` with message `retry`, then `ready`,
  then `cached` on every later call. Each RPC consumes one step of the
  sequence. A `WatchParcel` stream sends multiple messages within one RPC.
- `GetParcel(random)` picks `north` or `south`. The Go and YAML mocks use seed
  `42`, so a fresh mock reproduces the same series of ten choices.
- `Dispatch` uses the Go-only `Echo()` reply. The client sends `a` and reads it
  back, then does the same for `b`. Closing the send side leads to EOF.
