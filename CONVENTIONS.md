# Coding conventions

How to change **this** repository. Agent behavior is in [`AGENTS.md`](AGENTS.md).

These override the `use-modern-go` and `uber-go-style` skills. Apply them to code you write or change. `[lint]` means `make lint` checks it.

## After every edit

Run the checks in this order and fix failures before calling the task done. First match wins.

- **Docs only**: every file the task changed is `*.md`.
  1. `go test -run TestAgentDocs .` when an agent doc or a skill changed. Nothing else.
- **Anything else**:
  1. `go tool gofumpt -l .` prints nothing. `make fmt` fixes it.
  2. `go mod tidy -diff` prints nothing. `make tidy` fixes it.
  3. `make lint`
  4. `make test`

One file that is not `*.md` means the task is not docs only. CI (`.github/workflows/ci.yml`) runs the checks above on every change but one to `*.md` files outside the agent docs.

## Code shape

- One struct field per line, test-table structs included. Never `a, b string`.
- Prefix unexported package-level vars and consts with `_`, e.g. `_defaultProxyConfig`.
- Assert interface compliance at the top of the file: `var _ I = (*T)(nil)`.
- File names are lowercase, as Go's own are: `reply_random.go`, `hasprefix.go`. Never end one in a GOOS or GOARCH name (`_js.go`, `_linux.go`): Go builds it only there.
- Keep one short receiver name per type [lint]: `app` (`Server`, `Base`), `cb` (`ConfigBuilder`), `b` (builders), `m` (`Mock`).
- Constructors are `New(...Option) (*T, error)` plus a panicking `MustNew`. The root package uses `New<Type>`. A `Must` prefix means the function panics.
- Imports come in three groups separated by blank lines [lint]: standard library, third-party, then `github.com/caffeine-projects/vlatte/...`. Uber uses two.
- Only `admin`, `internal/admin/...`, `cmd/...` and tests may import the admin server or the MCP SDK [lint].

## Errors

- Messages are lowercase [lint] and start with their scope, e.g. `store: …`, `config: …`.
- Wrap with `fmt.Errorf("<context>: %w", err)`. Older `"\n%w"` and `". %w"` forms stay until their line changes.
- Sentinels are exported `Err…` vars built with `errors.New`, grouped in `var (…)`. Unexported error vars are `err…`, never `_`-prefixed [lint].
- Static messages use `errors.New`, not `fmt.Errorf` [lint].
- Match error types with `errors.AsType[T](err)`. Never `errors.As` [lint].
- Panic only in `Must*` functions, constructors given programmer-error input, and schema declaration bugs. Return errors everywhere else.
- errcheck is off on purpose.

## Tests

- **Test behavior, not implementation.** Call the code the way its caller does and assert what that caller observes: a return value, an error and its type, a response, an event. Never assert unexported state or the order of internal calls. A refactor that keeps behavior keeps every test green.
- **Every assertion can fail.** Assert the exact value: `require.Equal(t, 3, n)`, not `require.Positive(t, n)`; `require.ErrorIs` or `errors.AsType` on an error, not `require.Error` alone. A test that still passes with the behavior it names broken is wrong. Name a test after the behavior and why it matters, not after the function it calls.
- **A file has one test file, beside it and named after it**: `reply_seq.go`, `reply_seq_test.go`. Its properties, fuzz targets and benchmarks go there too; never `_prop_test.go`, `_gen_test.go`, `_internal_test.go` or `_bench_test.go`. Generators go at the bottom, after a `// Generators` comment. A test spanning several files of a package goes in its `test/` folder.
- **A test that drives a whole HTTP server** through the public API, across features, goes in `mockhttp/e2e/`.
- **Fix a bug test-first.** Before changing code for a bug, or for an issue a review or a tool reports, write the test that reproduces it and run it. It must fail, and for the reason reported, not a typo or a missing import. A test you cannot make fail means the issue is not what you thought: stop, say so, and change nothing. Then fix it, see the same test pass, and keep the test in the change.
- **A rapid failure becomes a named regression test.** rapid writes failfiles to `testdata/rapid/`, which git ignores: turn each into a test named after the case it found.
- An existing test keeps its place. Move one only when asked.

### Time in tests

A test that sleeps for something to happen fails once the machine is busy: `make test` runs every package at once, with the race detector. Wait on what the test asserts, never on a stretch of time standing in for it.

- That something happened: the code under test signals a channel or a `sync.WaitGroup`, and the test waits on it.
- That time passed: run the test in `synctest.Test` from `testing/synctest`, where the clock is fake and moves only when every goroutine in the bubble is blocked. Swap the network for an in-memory transport there, as `webhook/webhook_test.go` does with `webhook.WithTransport`: a goroutine waiting on a real socket keeps the clock still.
- Tests that `time.Sleep` for an event are legacy. Do not copy them.

## Documentation

Doc comments are the contract pkg.go.dev publishes. Write them for the caller.

- Start with the name, in a full sentence: `// Seq creates a new SequentialReply.`
- Say what the signature does not: behavior that surprises, how sibling APIs differ, the errors a caller must handle, what a parameter means when its name does not say. Do not restate the types or narrate the implementation.
- No design history or changelogs. An observable why stays when it is the contract.
- Link identifiers with doc links: `[Equal]`, `[vlatte.Matcher]`, `[github.com/caffeine-projects/vlatte/mockhttp]`.
- Every API a doc comment, an `Example` or the README shows exists today, with the signature shown. `Example` functions compile and run in `make test`; README snippets do not, so compare them with the code.

## Third-party APIs

Before relying on the API of a module in `go.mod`, read it at the version this repository uses: `go doc <import path>.<Symbol>` from the repository root. Do not write it from memory: the version here may be newer than what you know. The same goes for a standard library symbol you are unsure of.

## Security

- Pin every GitHub Action to a full commit SHA with the version as a trailing comment: `uses: actions/checkout@<sha> # v7.0.1`. Never a tag or a branch.
- Never disable, skip, narrow or loosen a security workflow or a CI check to make CI pass. Report the failure instead.
- Secrets never enter the repository, a prompt, or a log line.
