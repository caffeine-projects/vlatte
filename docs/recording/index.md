# Recording

Record a real service once, then test without it. A recording sends requests to the service, writes what it answered as [mock files](/http/mock-files), and the server answers the same requests the same way from then on: in later test runs, in CI, and with no network.

::: code-group

```go [HTTP]
err := m.StartRecording(
	mockhttp.RecordDir("testdata/recorded"),
	mockhttp.RecordTarget("https://api.example.com"),
)
// ... requests to m reach the target, and are recorded ...
res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

```go [gRPC]
err := m.StartRecording(mockgrpc.RecordTarget("localhost:50051"), mockgrpc.RecordDir("testdata/grpc_recorded"))
// ... calls to m reach the target, and are recorded ...
res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

```sh [CLI]
vlatte --record --record-target=https://api.example.com
# Ctrl-C writes the recordings; the next run serves them
vlatte
```

:::

## How it works

1. **Take.** With a target, the requests the recording takes go to the target before any mock. Without one, it takes what the server proxies already. Filters choose which requests it takes.
2. **Write.** Each answer becomes a mock file that matches its request, and no other, in `testdata/vlatte/_mocks_recorded` by default for HTTP, and `_mocks_recorded` under the gRPC root directory for gRPC.
3. **Play back.** Stopping the recording serves its files, and a server started later serves them too: they are mock files like any other, which you review and commit.

| Page | What it covers |
|---|---|
| [HTTP](/recording/http) | What is recorded, filters, playback, what a recording matches and answers. |
| [gRPC](/recording/grpc) | How gRPC calls, streams included, are recorded and played back. |
| [CLI, settings and admin API](/recording/cli-admin) | Recording from the command line, from settings, and while a server runs. |
| [Files and secrets](/recording/files-secrets) | Where recordings go, and keeping secrets out of them. |

The [Record and replay](/guides/record-and-replay) guide builds a test helper that records on demand and replays otherwise.
