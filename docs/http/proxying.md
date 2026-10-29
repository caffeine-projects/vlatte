# Proxying

vlatte sends requests on to real servers in three ways:

- a **proxy reply**: a mock that answers with what its target answers, `From(target)` in Go and the `proxy` key in mock files;
- the **forward proxy**: clients use the server as their HTTP proxy, `Setup().Proxy()` in Go and the `proxy` setting, and what no mock matches goes to its host;
- the **recording target**: see [Recording and playback](/recording/).

## How proxies answer

The three share one engine, and answer alike:

- The body streams as the upstream sends it, trailers included. A body of unknown length, or an event stream, reaches the client as it comes.
- The hop-by-hop headers of either connection, such as `Connection`, `Keep-Alive` and `Proxy-Authorization`, don't go on. A request's `Te: trailers` does.
- The upstream's `VLatte-*` headers, such as the debug headers of an upstream that is a vlatte server too, don't reach the client: it gets the proxy's.
- Compressed bodies go on as they came, and a redirect goes back to the client: the proxy doesn't follow it.
- A request that asks to switch protocols, such as a WebSocket handshake (`Connection: Upgrade`, `Upgrade: websocket`), asks the upstream too. Once the upstream switches, the client gets its `101`, and the two connections are piped until both sides end, or the server closes, while the mock's callbacks and events run. A mock counts the call, and the log shows it, when the switch is done. A mock's pipes apply to the connection too: see [Pipes](/http/pipes). A mock with response mappers doesn't ask the upstream to switch, as they can't apply to a connection: the upstream answers as to any request. `h2c`, which asks for HTTP/2 on the client's own connection, isn't asked on. An upstream that switches to a protocol the client didn't offer fails the call. A switch needs HTTP/1.1.
- The timeout bounds the wait for the response headers, connecting and sending the request included: 30 seconds by default, and 0 waits forever. The body has no time limit.
- A call that fails gets the server's answer to a failure, `ErrorStatusCode` with `VLatte-Error: reply`, unless the client left: then the log warns. Errors and logs show the upstream's URL without its password, and with its query values masked: `?api_key=xxxxx`. An upstream body that breaks off cuts the client's connection, so the client doesn't take part of it for all of it.
- A request the proxy sends on carries the server's ID in `VLatte-Via`. A request that passes the same server 10 times, as in a loop, such as through a proxy reply whose target is the server itself, fails as a proxy loop rather than being sent on forever. A route may pass a server more than once, such as through a gateway that sends it back.
- What the proxy keeps of a body, for the log, callbacks and events, is at most 1 MiB, and only when one of them reads it.
- A request body the server read whole, shorter than `MaxBodyParsingLimit` and without `NoBodyParsing`, can be sent again: the transport resends a replayable request, such as one with an `Idempotency-Key`, on a new connection when the one it reused turned out closed.

## Proxy replies

The target's path and query come before the request's: with `target: http://api.local/v1?tenant=a`, a request to `/users?page=2` goes to `http://api.local/v1/users?tenant=a&page=2`. `trim_prefix` and `trim_suffix` cut the request path first: `trim_prefix` cuts whole segments, so `/api` cuts `/api/users` to `/users` and leaves `/apiary` as it is, and `trim_suffix` cuts text, such as an extension: `.json` cuts `/users.json` to `/users`. When the target has a path, a request path with a `.` or `..` segment, escaped or not, fails: it could leave that path. `request_header` sets headers of the request the target gets, in place of the client's values of the same name, and `remove_request_headers` leaves request headers out; `header` sets headers of the response, in place of the target's. `x_forwarded: true` (`XForwarded()` in Go) tells the target about the client: its address in `X-Forwarded-For`, after those of the proxies before, the host it asked for in `X-Forwarded-Host`, and its scheme in `X-Forwarded-Proto`. `preserve_host: true` (`PreserveHost()`) sends the target the `Host` the client asked for, in place of the target's own. `timeout` is the timeout. A mock's `delay` comes before the call: a client that leaves during it gets nothing, and the target isn't called. A mock with response mappers reads the whole body first, so they see it in `Body`: up to 1 MiB, the most a proxy keeps of a body. A larger body fails the reply, and so does an event stream (`text/event-stream`) at once, as it doesn't end; a stream of another type that never ends waits for the client to leave. A mock's pipes can change the body's length: the target's `Content-Length` doesn't go on. A mock's description, which the admin API serves, keeps the target as it was set, credentials included, so it loads back as the same mock.

The target's TLS certificate isn't verified unless `ssl_verify: true`, so a target with a self-signed certificate answers. Set it for a target that gets credentials, such as `https://user:pass@api.local`, whose user info goes as Basic auth: over `http`, it goes in cleartext. `ssl_verify` also decides whether the certificate of an `https` proxy on the way, from `HTTPS_PROXY`, is verified.

## Proxy all

`ProxyAll(From(target))` in Go mocks what no other mock matches with a proxy reply: it matches any method and path, and comes last, at priority `math.MaxInt`. It leaves alone `CONNECT`s, and, on a server that is a forward proxy, the requests in absolute form, which the proxy sends on to their hosts. In a mock file, `proxy_all` takes the keys of `proxy` and makes the same mock, without `request`; a `ProxyAll` mock's description loads back as one. The `proxyall` recipe mocks it. The `proxy_all` setting does the same without a mock, so assertions don't count it, and `Clean` and `Reload` leave it: `vlatte --proxy-all-target=https://api.example.com`. It takes the keys of a mock file's `proxy` but `request_header` and `header`, which flags and variables can't give.

## Forward proxy

A client sends its requests to the server in absolute form, `GET http://api.local/users`, and opens a tunnel with `CONNECT` for an `https` URL. A mock that matches the request answers it; mocks match the path and ignore the host, unless they match it, with `host` or an absolute `url`. A request no mock matches goes to its host. A request in origin form, `GET /users`, names no host: no mock matching it, it gets the no-match answer. `proxy.timeout` is the timeout, and `proxy.ssl_verify` verifies the certificates of the hosts, and of an `https` proxy on the way, `proxy.via` or the one of `HTTPS_PROXY`: none is verified by default. `proxy.via` sends the requests through another proxy, an `http`, `https`, `socks5` or `socks5h` URL whose user info goes as its credentials: a client's `Proxy-Authorization` is for vlatte, and doesn't go on.

A `CONNECT` no mock matches opens a tunnel to its host, through `proxy.via` or the proxy of the environment, if any, within `proxy.timeout`, and the log shows that it opened. What goes through a tunnel, such as a TLS connection, isn't read: mocks don't see it, unless the proxy intercepts it. Each side of a tunnel ends what it sends on its own, and the tunnel closes once both did, or once the server closes. A tunnel needs HTTP/1.1, and a response writer that can hijack its connection: a middleware's writer must unwrap, with `Unwrap() http.ResponseWriter`, to the server's.

## HTTPS interception

With `proxy.intercept` (`ProxyConfig{Intercept: true}` in Go), the proxy serves the TLS of a `CONNECT` no mock matches itself. It answers with a certificate for the host the `CONNECT` names, or the name the client asks for, signed by its CA, which clients must trust. The requests within are then the server's own, for `https://` that host, which `scheme` and an absolute `url` see: mocks answer them, recordings record them, the log shows them, and the proxy sends the rest on to the host. A connection that doesn't start with TLS goes to the host through a tunnel, as does one whose client offers only `h2` by ALPN, such as a gRPC client, and one whose client sends nothing for 2 seconds, waiting for a host that speaks first, such as an SSH server: a host the proxy can't reach then closes the connection, as the `200` went out already. `proxy.ca_cert` and `proxy.ca_key` name the CA's files, which the settings need: a CA's certificate, or an intermediate CA's, followed by its chain, which the proxy sends with each host's. In Go, `ProxyConfig.CA` sets it, and without one, the server makes a CA of its own, valid for a year, which `m.ProxyCA()` returns for clients to trust:

```go
m := mockhttp.MustNew(mockhttp.Setup().Proxy(&mockhttp.ProxyConfig{Intercept: true}))
pool := x509.NewCertPool()
pool.AddCert(m.ProxyCA())
client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}}
```

An intercepted connection speaks HTTP/1.1.

## Transports

In Go, `From(target).Transport(rt)` sends a proxy reply through a transport of yours, such as one with a client certificate, and `Setup().Transport(rt)` sets one for every proxy reply, the recording target, the forward proxy and webhooks. A transport of yours replaces the one vlatte makes, and decides on its own how it verifies certificates and which proxy it goes through: `ssl_verify`, `proxy.via`, `proxy.ssl_verify` and `record.skip_ssl_verify` fail with it, rather than being ignored. The timeouts apply to any transport.

The transports vlatte makes go through the proxy of `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, which leaves out `localhost`, unless `proxy.via` sets one. Docker can set these variables in containers from `~/.docker/config.json`, and so send the hosts of a compose network, such as `http://api:8080`, to that proxy: add them to `NO_PROXY`. A variable that names the vlatte server itself sends the requests it proxies back to it, until they fail as a proxy loop: set `NO_PROXY=*` for its process, or `proxy.via`.

## Outbound rules

`outbound.deny` lists address ranges the server never connects to, and `outbound.allow`, once set, the only ones it may: proxy replies, the recording target, the forward proxy and its tunnels, and webhooks. Deny wins over allow. A server that others reach, as in Docker, keeps them from using its mocks to reach hosts they shouldn't, such as cloud metadata at `169.254.169.254` or an internal network:

```sh
vlatte --outbound-deny=169.254.0.0/16,10.0.0.0/8
```

The ranges are in CIDR form, `VLATTE_OUTBOUND_DENY` sets them too, and in Go `mockhttp.Setup().OutboundDeny(netip.MustParsePrefix("169.254.0.0/16"))`. They check the address the server connects to, once a host name resolves, so a name that resolves into a refused range is refused, and either one refuses `0.0.0.0` and `::`, which reach the server's own host. An IPv4 range holds no IPv6 address: deny `::1/128` with `127.0.0.0/8`, and `fd00:ec2::254/128` with `169.254.0.0/16` on AWS. Through a proxy on the way, `proxy.via` or the one of the environment, they check that proxy's address. A refused connection fails with `vlatte.ErrOutboundRefused`, which the answer's body names. A transport of yours, `Setup().Transport(rt)` or `From(target).Transport(rt)`, dials on its own, and they don't apply to it. gRPC mocks have `grpc.outbound.allow` and `grpc.outbound.deny`, which connect proxy replies and the recording target directly once set, never through the proxy of the environment, and don't apply to a target on a unix socket.
