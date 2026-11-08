# Generating mocks

`vlatte openapi` reads a spec and writes its mock files to a folder:

```sh
vlatte openapi --in petstore.yaml --out testdata/vlatte/_mocks
```

| Flag | Meaning |
|---|---|
| `--in` | The spec: a file or an `http(s)` URL, in JSON or YAML. Required. |
| `--out` | The folder to write the mock files to, created if missing. Required. |
| `--log-level` | What the command logs on standard error: `info`, the default, says what it is doing; `warn` only what it can't honor. |

It exits with `0` when it wrote the files, `1` when the spec can't be read or the files can't be written, and `2` for a missing or wrong flag.

## The files it writes

```text
Wrote 85 mock files, and the spec they check requests against, Modern-Petstore.6e8fa666.openapi.yaml, to testdata/vlatte/_mocks.
```

Each mock has a file of its own, named after the path, the method, the status and the media type of its reply, such as `pets-_id-get-200-json.mock.yaml` for `GET /pets/{id}`. Each starts with a comment naming the spec it came from:

<<< @/../cmd/vlatte/testdata/openapi/petstore/users-_id-get-200-json.mock.yaml{yaml}

Beside them is a copy of the spec, named after its title and a hash of its content, such as `Modern-Petstore.6e8fa666.openapi.yaml`. The mocks that [refuse requests that break the spec](/openapi/contract) check requests against it: keep it in the same folder, and commit it with them.

When `--out` is not `testdata/vlatte/_mocks`, the folder `vlatte` serves by default, the command prints how to serve them:

```sh
vlatte openapi --in petstore.yaml --out mocks
# Serve them with: vlatte --mock-files 'mocks/*.mock.yaml'
```

## The log

The command logs on standard error what it does, and what the spec asks that it can't honor, such as an example that doesn't fit its schema:

```text
2026-11-08T22:12:55Z INF reading the spec
2026-11-08T22:12:55Z WRN the example does not fit its schema: converted 12345 at /n to "12345" example=api.yaml#/paths/~1a/get/responses/200/content/application~1json/example
2026-11-08T22:12:55Z INF generating the mocks openapi=3.1.0 operations=2 schemas=0 title=Shop
2026-11-08T22:12:55Z INF generated the mocks mocks=8 took=0s warnings=1
```

Each warning names where in the spec it applies. A warning never stops the command: it writes every mock it can. [Troubleshooting](/openapi/troubleshooting#warnings) explains the common ones. In CI, `--log-level=warn` keeps only those.

## Running it again

Run the command again whenever the spec changes. Mock files are generated, so change the spec, or [add a mock by hand](/openapi/replies#changing-a-reply), rather than edit them.

A rerun overwrites the files it wrote before from a spec of the same title, and never touches other files in the folder, so hand-written mocks can share it. When the new spec no longer gives a file it wrote before, such as one for an operation you removed, it lists that file:

```text
These files were generated from this spec, but no longer are, and the mocks among them still load: Bookshop.18b623a9.openapi.yaml, books-post-201-json.mock.yaml, ...
```

The server still loads them: delete them when you no longer want them. The command finds its files by the title of the spec, so write two specs that share a title to two folders.

## Specs over HTTP

`--in` takes a URL as well as a file:

```sh
vlatte openapi --in https://example.com/openapi.json --out mocks
```

Each fetch, of the spec or of a file it refers to, times out after 30 seconds. A spec fetched over `http(s)` may refer only to other `http(s)` URLs, never to files on your machine.

## With Docker

The [Docker image](/run/docker) runs the command as well. Mount the folder that holds the spec, and run as its owner, so the files written belong to you:

```sh
docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/vlatte" \
  ghcr.io/caffeine-projects/vlatte openapi --in petstore.yaml --out testdata/vlatte/_mocks
```

## In CI

Mock files are plain text, and the same spec writes the same files on every run: commit them, and review changes to them as the spec changes. To check that committed mocks match the spec, run the command in CI and fail when `git status --porcelain` lists a change.
