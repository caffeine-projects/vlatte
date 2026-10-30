# Recording from the CLI

The `vlatte` command records a service until it stops, and serves the recordings on the next run:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Send the app's requests to the mock server, rather than directly to the API.
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded.
vlatte
```

Wait for the first command to finish writing before starting playback. Another Ctrl-C quits at once. Review the files for [secrets](/recording/files-secrets#secrets) before committing them.

`--grpc-record-*` flags record the gRPC server the same way. `vlatte --help` lists every flag, as does the [settings reference](/reference/settings).

For repeatable options, use [recording settings](/recording/settings). To start or stop a recording while the server keeps running, use the [admin API](/recording/admin-api).
