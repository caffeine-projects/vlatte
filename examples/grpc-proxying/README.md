# Proxying gRPC calls

How can a mock answer one parcel lookup locally and forward the rest to an upstream server?

Open [proxy_test.go](proxy_test.go) for the Go setup and [proxy.mock.yaml](testdata/proxy.mock.yaml) for the equivalent file mocks. Run both from the repository root.

```sh
go -C examples test -v ./grpc-proxying
```

- `GetParcel(local)` returns `cached` from the front server. Its exact mock has priority 0, so it wins before the priority-10 fallback.
- `GetParcel(P1)` goes upstream with `x-tenant` replaced by `demo` and `x-drop` removed. The client receives `forwarded` with response header `x-hop: upstream`.
- `WatchParcel(P2)` forwards the `accepted` update, then returns `Unavailable("feed offline")` with trailer `x-resume: 1`.

The YAML target is `127.0.0.1:50052`. The test substitutes the upstream's ephemeral loopback address in a temporary copy.
