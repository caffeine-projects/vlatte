# TLS and CORS

## TLS

`m.StartTLS()` starts the server as `Start` does, serving HTTPS, and `m.URL()` starts with `https://`.

| Option | Sets |
|---|---|
| `Setup().TLSCertKeyPair(certFile, keyFile)` | The certificate, from PEM files. |
| `Setup().TLSCertificates(certs)` | The certificates, as `tls.Certificate` values. |
| `Setup().TLSMutual(certFile, keyFile, clientCAFile)`, `Setup().TLSClientCAs(pool)` | Mutual TLS: clients present a certificate those CAs signed. |
| `Setup().TLSConfig(c)` | The whole TLS configuration. Other TLS options are then ignored. |

Without a certificate, the server serves the test certificate of `net/http/httptest`, which clients must trust or not verify:

```go
m := mockhttp.MustNew()
m.MustStartTLS()
t.Cleanup(m.Close)

client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
```

The `vlatte` command serves HTTPS with `--https`, on port `8443` in [Docker](/run/docker), and its certificate with `tls.cert` and `tls.key` (`--tls-cert`, `--tls-key`); `tls.ca` (`--tls-ca`) requires client certificates signed by its CAs.

For TLS between the server and the services it proxies to, see [Proxying](/http/proxying): `ssl_verify` on proxy replies, and [HTTPS interception](/http/proxying#https-interception) for the forward proxy.

## CORS

`Setup().CORS(...)` answers CORS requests, preflights included, with the headers of its configuration. `mockhttp.CORS()` builds one, and `mockhttp.DefaultCORSConfig` is the default: any origin, the methods `GET`, `POST`, `HEAD`, `PUT`, `DELETE` and `PATCH`, and `204` for a preflight.

```go
m := mockhttp.MustNew(mockhttp.Setup().CORS(mockhttp.CORS().
	AllowOrigin("https://app.example.com").
	AllowCredentials(true).
	ExposeHeaders("X-Request-Id")))
```

| Go | Setting | Sets |
|---|---|---|
| `AllowOrigin(origins...)` | `cors.allowed_origin` | `Access-Control-Allow-Origin`. |
| `AllowMethods(methods...)` | `cors.allowed_methods` | `Access-Control-Allow-Methods`. |
| `AllowedHeaders(headers...)` | `cors.allowed_headers` | `Access-Control-Allow-Headers`. |
| `ExposeHeaders(headers...)` | `cors.expose_headers` | `Access-Control-Expose-Headers`. |
| `AllowCredentials(v)` | `cors.allow_credentials` | `Access-Control-Allow-Credentials`. |
| `MaxAge(seconds)` | `cors.max_age` | `Access-Control-Max-Age`. |
| `SuccessStatusCode(code)` | `cors.success_status_code` | The status of a preflight. |

In settings, `cors: true` (`--cors`) turns CORS on with the defaults, and setting any `cors` key turns it on too.

An answer that sends a CORS header, such as a proxied one, answers CORS by itself: the server's CORS headers aren't sent with it. A `Handler` reply writes its answer on top of the server's CORS headers: it should `Set` its CORS headers, or `Del` the server's. Preflights the server answers aren't [journaled](/features/journal).
