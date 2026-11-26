# TLS and mutual TLS for gRPC

How can a parcel client verify the mock server's identity, and how can the server also verify the client?

Open [tls_test.go](tls_test.go) for the server and client configuration and [lookup.mock.yaml](testdata/lookup.mock.yaml) for the file mock. Run both the Go and YAML cases from the repository root.

```sh
go -C examples test -v ./grpc-tls
```

With `MustStartTLS`, a client that trusts the server certificate receives `in_transit` for `GetParcel(P1)`. A plaintext client gets `Unavailable`. With `TLSMutual`, the client also needs an identity the server trusts. The same client trust roots without the client certificate produce `Unavailable`.

## Try mutual TLS with the CLI

Install `protoc` and `grpcurl` for this walkthrough. From the repository root, generate the parcel service descriptors and start VLatte with the public test certificates.

```sh
mkdir -p .local/grpc-examples
protoc -I examples/grpc-rpc-types/pb --include_imports \
  --descriptor_set_out=.local/grpc-examples/parcels.binpb parcels.proto

certs=mockgrpc/testdata/cert
go run ./cmd/vlatte --addr=127.0.0.1:0 --admin-addr=127.0.0.1:0 \
  --workdir=.local/grpc-examples --grpc-addr=127.0.0.1:50051 \
  --grpc-descriptors=.local/grpc-examples/parcels.binpb \
  --grpc-mock-files=examples/grpc-tls/testdata/lookup.mock.yaml \
  --grpc-tls-cert="$certs/cert.pem" --grpc-tls-key="$certs/key.pem" \
  --grpc-tls-ca="$certs/cert_client.pem"
```

From another terminal at the repository root, trust the server with `-cacert` and present the client identity with `-cert` and `-key`. The reply contains `"state": "in_transit"`.

```sh
certs=mockgrpc/testdata/cert
grpcurl -cacert "$certs/cert.pem" \
  -cert "$certs/cert_client.pem" -key "$certs/key_client.pem" \
  -d '{"id":"P1"}' 127.0.0.1:50051 parcel.v1.Tracking/GetParcel
```
