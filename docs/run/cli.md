# CLI

`vlatte` serves the mock files under `testdata/vlatte/_mocks` in the working directory:

```sh
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/hello
```

Without `--addr`, it listens on a random port and prints the URL. Settings can also come from a `.vlatterc` file or `VLATTE_*` environment variables; see [Configuration](/run/configuration). Run `vlatte --help` to list them, and `vlatte completion --help` to set up shell completion.

Mock files can send [webhooks](/webhooks/): the CLI registers the `webhook` listener, and `--webhook-timeout` bounds each request that sets no `timeout` of its own.

It [records](/recording/) a service until it stops, and serves the recordings on the next run:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/vlatte/_mocks_recorded; another Ctrl-C quits at once
vlatte
```

It serves the [admin API](/admin-api/) too, on an address of its own, which it prints: a random local port unless `--admin-addr` sets one, and `--admin=false` turns it off. With `VLATTE_DOCKER_HOST` set, it listens on port 8081 of that host, as the mocks do on 8080. `POST /mocks` adds a mock sent in the request body, for example as JSON:

```sh
vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/added"},"response":{"status":201}}' \
  http://127.0.0.1:8081/mocks
```

The command keeps the mocks its admin API adds in `{workdir}/_admin`, as [persistent admin mocks](/admin-api/#persistent-admin-mocks), so the next run serves them: `--admin-dir` moves them, and `--admin-dir=""` keeps them in memory.

It serves gRPC mocks too, when a setting of the `grpc` section is set, as `--grpc-*` flags, `VLATTE_GRPC_*` variables, or `grpc:` in `.vlatterc`. It listens on `:50051`, unless `--grpc-addr` says, serves the services of the descriptor sets `--grpc-descriptors` names, from the mock files `--grpc-mock-files` names, with server reflection, an admin API of its own, which it prints, and a journal of its calls:

```sh
protoc --descriptor_set_out=api.binpb --include_imports api.proto
vlatte --grpc-descriptors=api.binpb --grpc-mock-files='mocks/grpc/*.yaml'
grpcurl -plaintext -d '{"id": "u-1"}' localhost:50051 users.Users/Get
```

The gRPC admin API keeps the mocks it adds in `{workdir}/_grpc_admin`, as [persistent admin mocks](/admin-api/#persistent-admin-mocks), so the next run serves them: `--grpc-admin-dir` moves them, and `--grpc-admin-dir=""` keeps them in memory.

`--grpc-tls` serves them over TLS, with a self-signed certificate, unless `--grpc-tls-cert` and `--grpc-tls-key` name the files of one; `--grpc-tls-ca` requires clients to present a certificate signed by a CA of its file. Each of these turns TLS on.

It keeps a [journal](/more/journal) of the requests it serves in `{workdir}/_journal`, across restarts, which the admin API queries: `--journal-dir` moves it, `--journal-dir=""` keeps it in a temporary directory, and `--journal=false` turns it off. Two commands can't share a journal directory: give each a `--name`, which gives it a [work directory](/run/configuration#work-directory) of its own, `testdata/vlatte/<name>`. The journal holds the requests it got, secrets included: its directory ignores its files in git.
