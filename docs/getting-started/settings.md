# Settings

A server's settings, such as its address, its mock files or its log level, come from four places, each overriding the ones before it:

1. Code: `mockhttp.Setup()` or `mockgrpc.Setup()`, such as `Setup().Addr(":8080")`.
2. A `.vlatterc.yaml`, `.vlatterc.yml` or `.vlatterc.json` file in the working directory or in `testdata`.
3. `VLATTE_*` environment variables.
4. Command-line flags, for the `vlatte` command.

Each setting has one key that the others derive from: `log.level` is nested under `log` in a file, as below, `VLATTE_LOG_LEVEL=debug` in the environment and `--log-level=debug` on the command line.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
```

Servers in tests read the file and the environment too, unless `Setup().ConfigSources()` says otherwise.

- [Configuration](/run/configuration) covers the sources in full: how values are written, the root and work directories, and the sources in tests.
- The [settings reference](/reference/settings) lists every setting, with its flag, variable and file key.
