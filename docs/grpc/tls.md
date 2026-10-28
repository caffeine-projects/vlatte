# TLS

`m.StartTLS()` starts the server as `Start` does, serving TLS. `Setup().TLSCertKeyPair(certFile, keyFile)` or `TLSCertificates(certs)` sets its certificate, `TLSMutual(certFile, keyFile, clientCAFile)` or `TLSClientCAs(pool)` requires clients to present a certificate those CAs signed, and `TLSConfig(c)` sets the whole TLS configuration. Without a certificate, it serves a self-signed one for `localhost`, `127.0.0.1` and `::1`, which `m.Certificate()` returns for clients to trust. `m.Dial()` trusts the certificate the server serves; pass it `grpc.WithTransportCredentials` for a server that requires a client certificate. Credentials `ServerOptions` set with `grpc.Creds` win over these: `StartTLS` then serves them as `Start` does, `m.Certificate()` returns `nil`, and `m.Dial()` needs `grpc.WithTransportCredentials`.

The `vlatte` command serves gRPC over TLS with `--grpc-tls`: see the [CLI](/run/cli).
