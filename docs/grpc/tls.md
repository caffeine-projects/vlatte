# TLS

`m.MustStartTLS()` serves gRPC over TLS, with a self-signed certificate for `localhost`, `127.0.0.1` and `::1`, and `m.Dial()` already trusts it:

```go
m := mockgrpc.MustNew()
m.MustStartTLS()
t.Cleanup(m.Close)

client := pb.NewUsersClient(m.MustDial())
```

`m.Certificate()` returns the certificate for clients of your own to trust. Credentials `ServerOptions` set with `grpc.Creds` win over these: `StartTLS` then serves them as `Start` does, `m.Certificate()` returns `nil`, and `m.Dial()` needs `grpc.WithTransportCredentials`.

Certificates of your own, mutual TLS and the CLI's `--grpc-tls` are on the [TLS](/tls/) page, for HTTP and gRPC alike.
