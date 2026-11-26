# Verify gRPC requests

How many times did the client look up each parcel, including a lookup that
missed the mock? Open [verification_test.go](verification_test.go) and
[lookup.mock.json](testdata/lookup.mock.json) to see the same mock in Go and JSON.

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-verification
```

Two lookups for `P1` return `in_transit`. One lookup for `P2` returns
`NotFound`. Both runs enable the journal with `Journal("")`. The test queries
it with `VerifyRequests().Matching(...).Count(...)` and finds exactly two
requests for `P1` and one for `P2`.
