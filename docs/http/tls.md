# TLS

`m.MustStartTLS()` serves HTTPS, with no certificate to make: `m.URL()` starts with `https://`.

```go
m := mockhttp.MustNew()
m.MustStartTLS()
t.Cleanup(m.Close)
```

Its default certificate is the test certificate of `net/http/httptest`, which clients skip verifying, or you give the server one of yours with `Setup().TLSCertKeyPair(certFile, keyFile)`. Mutual TLS, the CLI's `--https`, Docker and the admin API are on the [TLS](/features/tls) page, for HTTP and gRPC alike.

For TLS between the server and the services it proxies to, see [Proxying](/http/proxying): `ssl_verify` on proxy replies, and [HTTPS interception](/http/proxying#https-interception) for the forward proxy.
