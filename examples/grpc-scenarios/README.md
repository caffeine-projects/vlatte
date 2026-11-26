# gRPC scenarios

How can each parcel move through its own delivery states? Open
[scenarios_test.go](scenarios_test.go) and [lifecycle.mock.yaml](testdata/lifecycle.mock.yaml)
for the Go and YAML mocks.

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-scenarios
```

The scenario key comes from the parcel ID. The first lookup for either parcel
moves its scenario from `started` to `in_transit`. The next lookup moves it to
`delivered`, where subsequent lookups keep it. The reply template reads the
state after the current call's transition.

Looking up `P1` three times yields `in_transit`, `delivered`, and `delivered`.
The first lookup of `P2` returns `in_transit`. The test then resets the `P1` key.
The next lookup of `P1` returns `in_transit`, and the next lookup of `P2` returns
`delivered`.
