# gRPC stream faults

What does a client see when it cancels while waiting for an update or when the
connection drops midstream? Open [faults_test.go](faults_test.go) and
[delay.mock.yaml](testdata/delay.mock.yaml).

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-faults
```

In the Go and YAML delay cases, `WatchParcel` sends the header `x-feed: open`
and waits an hour before sending the first update. The client waits for the
header, cancels immediately, and receives `Canceled` from `Recv()`.

The Go-only `Func` cases set `x-resume: 1`, send an `accepted` update, and cut
the connection with `ErrAbort` or `ErrReset`. The next `Recv()` returns
`Unavailable` from the transport, and the client receives no `x-resume`
trailer. For a stream that sends a final gRPC status and trailer, see the
[authored watch reply](../grpc-rpc-types/testdata/serverstream.mock.yaml).
