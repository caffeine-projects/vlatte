# Settings

A server reads settings such as its address and log level in this order. Later sources override earlier ones.

1. Code: `mockhttp.Setup()` or `mockgrpc.Setup()`, such as `Setup().Addr(":8080")`.
2. A `.vlatterc.yaml`, `.vlatterc.yml` or `.vlatterc.json` file in the working directory. The CLI also looks in `testdata`.
3. `VLATTE_*` environment variables.
4. Command-line flags, for the `vlatte` command.

Each setting has one key that the others derive from: `log.level` is nested under `log` in a file, as below, `VLATTE_LOG_LEVEL=debug` in the environment and `--log-level=debug` on the command line.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
```

In Go, `MustNew()` reads the settings. When you start the server it discovers fixtures under `testdata/vlatte/`. You can add more mocks with `MustMock` on the same server.

Use `Setup().ConfigFile("settings/test.yaml")` to select a config file or `Setup().MockFilePatterns("fixtures/*.yaml")` to select fixtures. An explicit Go pattern replaces the file's `mock_files` list and keeps its other settings. Chain both methods to select both paths.

For an isolated test, `Setup().SkipDefaults()` skips config lookup, environment settings and conventional fixtures. You can still select individual inputs, for example `Setup().SkipDefaults().ConfigFile("settings/test.yaml")`.

- [Configuration](/run/configuration) covers the sources in full: how values are written, the root and work directories, and the sources in tests.
- The [settings reference](/reference/settings) lists every setting, with its flag, variable and file key.
