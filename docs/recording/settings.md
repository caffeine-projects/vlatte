# Recording settings

The `record` section of the configuration records from server start to close:

```yaml
# .vlatterc.yaml
record:
  target: https://api.example.com
  paths:
    - /v1/*
  request_headers:
    - Accept
  exclude_response_headers:
    - Set-Cookie
    - X-Request-Id
```

Setting a key in `record` turns recording on. `record: false` turns it off. The `grpc.record` section configures gRPC recording. See [Configuration](/run/configuration) for precedence between code, files, environment variables and flags, and the [settings reference](/reference/settings) for every recording key.

In Go, `Setup().Record(...)` records from start to close too. For recording only part of a test, use `StartRecording` and `StopRecording`, as in the [record and replay guide](/guides/record-and-replay).

## Upstream TLS

The target's TLS certificate is verified: a recording sends the target its requests, credentials included. `record.skip_ssl_verify`, or `RecordSkipSSLVerify(true)`, skips verification, such as for a self-signed certificate.

In Go, `RecordTransport(rt)` sends through a transport of yours instead, such as one that trusts the target's CA. That transport decides whether it verifies the target: setting `skip_ssl_verify` with it fails the recording.

## Files and headers

`exclude_response_headers` replaces the default list, so include `Set-Cookie` to keep excluding it. Request headers named by `request_headers` become matching criteria in the recorded mocks. See [HTTP recording](/recording/http#what-a-recording-matches) and [Files and secrets](/recording/files-secrets) before saving credentials or personal data.
