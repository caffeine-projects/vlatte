# Manage gRPC mocks over HTTP

How do you turn a gRPC mock off and back on while the server is running?
Open [admin_test.go](admin_test.go) to follow the HTTP management requests
alongside the gRPC lookups. The JSON run posts
[express.mock.json](testdata/express.mock.json) to `/mocks` and receives HTTP 201.
The Go run installs the same mock with `MustMock`.

Run the example from the repository root.

```sh
go -C examples test -v ./grpc-admin
```

The first lookup for `express` returns `priority`. An HTTP PATCH to
`/mocks/express` disables the mock, so the same gRPC request returns `NotFound`.
A second PATCH enables the mock again and the lookup returns `priority`.
