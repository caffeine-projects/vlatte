# Recording and replaying gRPC calls

How can you replay an upstream's changing parcel status after the upstream has stopped?

In [recording_test.go](recording_test.go), `TestFileMocks` loads the upstream's responses from [upstream.mock.yaml](testdata/upstream.mock.yaml) and `TestProgrammaticMocks` registers them in Go. Both tests replay the recorded YAML files. Run from the repository root.

```sh
go -C examples test -v ./grpc-recording
```

The recorder forwards two identical `GetParcel(P1)` requests with `x-tenant: demo` and a `WatchParcel(P2)` request. After the watch ends, `StopRecording` writes two YAML mocks to a temporary directory. A fresh server loads those files after both the upstream and recorder have closed.

- Repeating the lookup returns `accepted`, `delivered`, then `delivered` again. The last recorded response repeats after the sequence ends.
- The watch sends `accepted` and ends with `Unavailable("feed offline")` and trailer `x-resume: 1`.
- Changing the tenant produces a mock miss because the recorder included `x-tenant` in the recorded request match.

A recorded bidirectional stream replays its captured outbound messages immediately. For interactive turn-taking, see the [authored Dispatch example](../grpc-rpc-types/README.md).
