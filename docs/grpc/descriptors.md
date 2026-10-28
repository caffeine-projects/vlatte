# Descriptors and reflection

A server without the generated code of a service serves it from its descriptors, with messages of dynamic types: `Setup().Descriptors("api.binpb")` loads descriptor sets, as `protoc --descriptor_set_out=api.binpb --include_imports` or `buf build -o api.binpb` write them, and `Setup().Files(files)` takes a `*protoregistry.Files`.

```sh
protoc --descriptor_set_out=api.binpb --include_imports api.proto
```

```go
m := mockgrpc.MustNew(mockgrpc.Setup().Descriptors("api.binpb").Reflection())
```

## Reflection

`Setup().Reflection()` serves server reflection, so `grpcurl` lists the services and describes their messages. The `vlatte` command serves it by default: `--grpc-reflection=false` turns it off.

```sh
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext -d '{"id": "u-1"}' localhost:50051 users.Users/Get
```
