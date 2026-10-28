# Recording gRPC

```go
err := m.StartRecording(mockgrpc.RecordTarget("localhost:50051"), mockgrpc.RecordDir("testdata/grpc_recorded"))
// ... calls to m reach the target, and are recorded ...
res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

A gRPC server records as an [HTTP one](/recording/http) does. With `RecordTarget`, the calls the recording takes go to the target before any mock; without one, it takes the answers of `From` mocks, and of every mock with `RecordIncludeMocked(true)`. `RecordMethods` names the methods it takes, as `Method` does, and `RecordRequestHeaders` the metadata its mocks match. A recording matches a call by its method, its messages and that metadata, and answers with the header, messages, trailer and status of the calls of that request in turn, as a `response_sequence`, then the last one again; `RecordRepeats(vlatte.RecordFirst)` keeps the first only. A bidi call matches by its method and metadata only, as bidi mocks do, and plays its messages back at once. `Setup().Record(...)`, the `grpc.record` section of the settings and the admin API's `/recording` routes record too, and a server serves the recordings of its `grpc.record.dir`, `_mocks_recorded` under its root directory by default.
