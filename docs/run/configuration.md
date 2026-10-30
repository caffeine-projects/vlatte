# Configuration

A server's settings come from these sources, each overriding the ones before it:

1. Defaults.
2. Code: the options passed to `mockhttp.New`, such as `mockhttp.Setup().Addr(":8080")`.
3. A `.vlatterc.json`, `.vlatterc.yaml` or `.vlatterc.yml` file in the working directory or in `testdata`. `--config` or `VLATTE_CONFIG` names another file.
4. `VLATTE_*` environment variables.
5. Command-line flags, for the `vlatte` command.

## Keys, variables and flags

Each setting has a key that its variable and flag derive from: `log.level` is nested under `log` in a file, as below, `VLATTE_LOG_LEVEL=debug` in the environment and `--log-level=debug` on the command line. `vlatte --help` lists every setting, as does the [settings reference](/reference/settings).

In a file, each setting has its type, as in mock files: `addr: ":8080"` in quotes, `max_age: 150` bare, and a list as a list. Environment variables and flags are text:

- bools read as `strconv.ParseBool` reads them;
- numbers are written in the JSON number grammar;
- lists are comma-separated.

`log.level` and `log.verbosity` take a name, in any case, such as `info`.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
proxy:
  timeout: 1.5s # or milliseconds: 1500
cors: true
```

Setting a key of a section (`cors`, `proxy`, `proxy_all`, `record`, `admin` or `journal`) turns the section on, and `false` turns it off.

## HTTP read timeouts

HTTP read timeouts are opt-in. They apply to the default HTTP backend and intercepted HTTPS connections:

- `read_header_timeout` bounds reading HTTP/1.x request headers. It does not impose a per-stream header timeout on HTTP/2.
- `read_timeout` bounds reading the entire HTTP/1.x request, including headers and body. In HTTP/2, it bounds reading the body after the headers arrive. Receiving another body chunk does not restart it.
- `idle_timeout` bounds waiting for the next request on a keep-alive connection after the response ends.

All three default to `0`. A zero header or idle timeout uses `read_timeout`; with all three at zero, reads have no timeout. These settings do not limit response writes, so delayed replies and active response streams can outlast them. A custom `BackendFactory` controls its own server timeouts.

Settings accept nonnegative Go durations, such as `5s`, or numbers in milliseconds. For example:

```yaml
read_header_timeout: 5s
read_timeout: 30s
idle_timeout: 60s
```

The same controls are `--read-header-timeout`, `--read-timeout` and `--idle-timeout`, or `VLATTE_READ_HEADER_TIMEOUT`, `VLATTE_READ_TIMEOUT` and `VLATTE_IDLE_TIMEOUT`. In Go, use `mockhttp.Setup().ReadHeaderTimeout(5*time.Second).ReadTimeout(30*time.Second).IdleTimeout(60*time.Second)`, or the corresponding `mockhttp.Config` fields. The Go API follows `net/http.Server` semantics, including negative header or idle values to disable their fallback to `ReadTimeout`.

## Root directory

`root_dir` is the directory that relative mock file patterns and record directories start at. Absolute ones are used as they are.

## Work directory

A server keeps its own files in its work directory: `testdata/vlatte`, or `testdata/vlatte/<name>` for a server `name` names, so servers that share a directory keep apart. `workdir` (`--workdir`, `Setup().WorkDir(dir)`) sets another, as is, from `root_dir`. Without it, a server's name is one folder, which doesn't start with `_`, as vlatte's own do.

```
testdata/
  vlatte/
    _mocks/            mock files
    _mocks_recorded/   recordings
    _admin/            the mocks the CLI's admin API adds
    _grpc_admin/       the mocks the CLI's gRPC admin API adds
    _journal/          the CLI's journal
    test/              a server named "test": its own _mocks, _admin, _journal...
```

A path setting that starts with `{workdir}`, such as `mock_files`, `record.dir`, `admin.dir`, `grpc.admin.dir` or `journal.dir`, starts at the work directory: the defaults do, as `--help` shows. Other relative paths start at `root_dir`, or `grpc.root_dir` for a `grpc` setting. In a YAML file, quote such a value: `dir: "{workdir}/recordings"`.

## Errors

Unknown keys, flags and `VLATTE_*` variables are errors, except `VLATTE_DOCKER_HOST`. A server that can't load its settings reports every problem at once:

```
server: loading the configuration sources: config: 2 problems
  env VLATTE_LOGLEVEL: unknown_key: VLATTE_LOGLEVEL is not a known setting; did you mean VLATTE_LOG_LEVEL?
  file .vlatterc.yaml: invalid_value: proxy.timeout must be milliseconds or a Go duration, not "10x"; such as 1500 or 1.5s
```

## Sources in tests

Servers in tests read the file and the environment too. Pick the sources with `ConfigSources`, or pass none to use code only:

```go
m := mockhttp.MustNew(mockhttp.Setup().ConfigSources())
m = mockhttp.MustNew(mockhttp.Setup().ConfigSources(mockhttp.ConfigFile("testdata/server.yaml")))
```

## gRPC settings

The settings of gRPC mocks are in the `grpc` section: `grpc.addr`, `grpc.descriptors`, `grpc.mock_files`, `grpc.record`, `grpc.admin` and the others, as `--grpc-*` flags and `VLATTE_GRPC_*` variables. In Go, `mockgrpc.Setup()` configures a gRPC server. See the [CLI](/run/cli) and the [settings reference](/reference/settings).

gRPC has connection controls suited to HTTP/2:

- `grpc.connection_timeout` bounds connection setup, including TLS and the HTTP/2 handshake. Zero keeps gRPC's existing default of 120 seconds.
- `grpc.max_connection_idle` sends GOAWAY after a connection has had no active RPCs for this long. Zero keeps gRPC's default of no idle limit. An active RPC or stream keeps the connection busy, even while it exchanges no messages.

Both settings accept nonnegative Go durations or milliseconds, through the same sources as HTTP settings:

```yaml
grpc:
  connection_timeout: 5s
  max_connection_idle: 60s
```

The flags are `--grpc-connection-timeout` and `--grpc-max-connection-idle`; the variables are `VLATTE_GRPC_CONNECTION_TIMEOUT` and `VLATTE_GRPC_MAX_CONNECTION_IDLE`. In Go, use `mockgrpc.Setup().ConnectionTimeout(5*time.Second).MaxConnectionIdle(60*time.Second)`, or the corresponding `mockgrpc.Config` fields. Explicit `grpc.ConnectionTimeout` or `grpc.KeepaliveParams` options passed through `ServerOptions` take precedence over these settings.

These controls do not impose a deadline on an active RPC or stream, or a separate limit on reading each RPC's headers or messages. RPC deadlines come from the client's context.

