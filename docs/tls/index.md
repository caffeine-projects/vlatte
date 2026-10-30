# TLS

A mock server serves HTTPS, or gRPC over TLS, with one call and no certificate to make. Bring your own certificate, or require client certificates, with one option more.

::: code-group

```go [HTTP]
m := mockhttp.MustNew()
m.MustStartTLS() // m.URL() starts with https://
t.Cleanup(m.Close)
```

```go [gRPC]
m := mockgrpc.MustNew()
m.MustStartTLS()
t.Cleanup(m.Close)

conn := m.MustDial() // trusts the server's certificate
```

```sh [CLI]
vlatte --https            # HTTPS mocks
vlatte --grpc-tls ...     # gRPC mocks over TLS
```

```sh [Docker]
docker run --rm -p 8443:8443 -e VLATTE_HTTPS=true vlatte:dev
```

:::

## The default certificate

The Docker example uses the [image built from `main`](/run/docker#building-the-image), as the rest of this site's current-source examples do.

Without a certificate of yours, each protocol serves one of its own:

| Protocol | Certificate | For clients |
|---|---|---|
| HTTP | The test certificate of `net/http/httptest`, for `127.0.0.1`, `::1` and `example.com`. | Skip verifying it, as `curl -k` and `InsecureSkipVerify` do, or give the server a certificate you trust. `m.URL()` names `127.0.0.1`, which the certificate covers. |
| gRPC | A self-signed certificate made at start, for `localhost`, `127.0.0.1` and `::1`. | `m.Dial()` trusts it. `m.Certificate()` returns it for other clients, to add to a `x509.CertPool`. |

```go
// HTTP: a client for the default certificate.
client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}

// gRPC: a client of your own that trusts the server's certificate.
pool := x509.NewCertPool()
pool.AddCert(m.Certificate())
conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(pool, "")))
```

## Your own certificate

The options are the same for HTTP and gRPC, on `Setup()`:

| Option | Sets |
|---|---|
| `TLSCertKeyPair(certFile, keyFile)` | The certificate, from PEM files. |
| `TLSCertificates(certs)` | The certificates, as `tls.Certificate` values. |
| `TLSMutual(certFile, keyFile, clientCAFile)` | The certificate, and mutual TLS: clients present a certificate the CAs of `clientCAFile` signed. |
| `TLSClientCAs(pool)` | Mutual TLS, with the CAs as a `x509.CertPool`. |
| `TLSConfig(c)` | The whole TLS configuration. The other TLS options are then ignored. |

```go
m := mockhttp.MustNew(mockhttp.Setup().TLSCertKeyPair("testdata/localhost.pem", "testdata/localhost-key.pem"))
m.MustStartTLS()
```

Tools such as [mkcert](https://github.com/FiloSottile/mkcert) make a local certificate your machine already trusts: `mkcert localhost 127.0.0.1`.

## Mutual TLS

With `TLSMutual` or `TLSClientCAs`, the server asks every client for a certificate, and refuses the handshake of a client without one its CAs signed:

```go
m := mockhttp.MustNew(mockhttp.Setup().
	TLSMutual("testdata/server.pem", "testdata/server-key.pem", "testdata/client-ca.pem"))
m.MustStartTLS()
```

A gRPC client of such a server dials with credentials of its own: `m.Dial(grpc.WithTransportCredentials(...))`. The [HTTPS and mTLS mocks](/guides/https-mtls) guide tests a client both ways.

## The CLI and Docker

| Setting | Flag | Does |
|---|---|---|
| `https` | `--https` | Serves the HTTP mocks over HTTPS, with the default certificate unless `tls.cert` is set. |
| `tls.cert`, `tls.key` | `--tls-cert`, `--tls-key` | The certificate, from PEM files. |
| `tls.ca` | `--tls-ca` | Mutual TLS: the CAs that sign client certificates. |
| `grpc.tls` | `--grpc-tls` | Serves the gRPC mocks over TLS, with a self-signed certificate unless `grpc.tls.cert` is set. |
| `grpc.tls.cert`, `grpc.tls.key`, `grpc.tls.ca` | `--grpc-tls-cert`, `--grpc-tls-key`, `--grpc-tls-ca` | The same, for gRPC. |

```sh
vlatte --https --tls-cert=certs/localhost.pem --tls-key=certs/localhost-key.pem
```

The [Docker](/run/docker) image serves HTTPS on port `8443`, in place of `8080`, with `--https` or `VLATTE_HTTPS=true`. Mount the certificate files and name them with `VLATTE_TLS_CERT` and `VLATTE_TLS_KEY`.

## The admin API

The admin API serves HTTPS of its own, apart from the mocks: `admin.TLS(config)` in Go, and `admin.tls.cert` with `admin.tls.key` (`--admin-tls-cert`, `--admin-tls-key`) for the command, whose `vlatte health` then asks over HTTPS and trusts that certificate alone. Credentials sent to it travel in plain text without TLS: see [Authentication](/admin-api/#authentication).

## Proxies and recordings

When a server sends requests on, how it treats the certificates of the services it reaches depends on what sends them:

- [Proxy replies](/http/proxying) and the forward proxy don't verify certificates by default, so a target with a self-signed one answers. `SSLVerify(true)`, or `ssl_verify: true`, turns verification on: set it for a target that gets credentials.
- A [recording](/recording/settings) verifies its target's certificate, as it sends the target credentials. `record.skip_ssl_verify` skips it.
- The forward proxy's [HTTPS interception](/http/proxying#https-interception) answers HTTPS a client sends through it with certificates its own CA signs, so mocks can answer it.
- `Setup().Transport(rt)` sends all of them through a transport of yours, such as one with a client certificate.
