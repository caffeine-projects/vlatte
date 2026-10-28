# CLI, settings and admin API

## CLI

The `vlatte` command records a service until it stops, and serves the recordings on the next run:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded; another Ctrl-C quits at once
vlatte
```

`--grpc-record-*` flags record the gRPC server the same way. `vlatte --help` lists them all, as does the [settings reference](/reference/settings).

## Settings

`Setup().Record(...)` records from start to close, and so does the `record` section of the config:

```yaml
# .vlatterc.yaml
record:
  target: https://api.example.com
  paths: ["/v1/*"]
  request_headers: [Accept]
  exclude_response_headers: [Set-Cookie, X-Request-Id]
```

The target's TLS certificate is verified: a recording sends the target its requests, credentials included. `record.skip_ssl_verify`, or `RecordSkipSSLVerify(true)`, skips it, such as for a self-signed certificate. In Go, `RecordTransport(rt)` sends through a transport of yours instead, such as one that trusts the target's CA, which decides on its own whether it verifies the target: `skip_ssl_verify` with it fails the recording. `vlatte --help` lists every `record` setting.

## Admin API

The [admin API](/run/admin-api) starts and stops recordings while the server runs: `POST /recording` takes the keys of the `record` section, `GET /recording` counts the calls, and `DELETE /recording` stops it and lists the files it wrote. Its `/recorder` page does the same from a browser.
