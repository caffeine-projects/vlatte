---
name: vlatte-conventions
description: vlatte's own Go conventions for code shape (struct fields, globals, receivers, constructors, imports) and errors (messages, wrapping, sentinels, matching, panics). Use when writing, changing or reviewing Go code in this repository. Overrides use-modern-go and uber-go-style.
---

# vlatte conventions

These override `use-modern-go` and `uber-go-style`. Apply them to code you write or change. `[lint]` means `make lint` checks it.

## Shape

- One struct field per line, test-table structs included. Never `a, b string`.
- Prefix unexported package-level vars and consts with `_`, e.g. `_defaultProxyConfig`.
- Assert interface compliance at the top of the file: `var _ I = (*T)(nil)`.
- File names are snake_case, e.g. `reply_random.go`.
- Keep one short receiver name per type [lint]: `app` (`Server`, `Base`), `cb` (`ConfigBuilder`), `b` (builders), `m` (`Mock`).
- Constructors are `New(...Option) (*T, error)` plus a panicking `MustNew`. The root package uses `New<Type>`. A `Must` prefix means the function panics.
- Imports come in three groups separated by blank lines [lint]: standard library, third-party, then `github.com/caffeine-projects/vlatte/...`. Uber uses two.

## Errors

- Messages are lowercase and start with their scope, e.g. `store: …`, `config: …`.
- Wrap with `fmt.Errorf("<context>: %w", err)`. Older `"\n%w"` and `". %w"` forms stay until their line changes.
- Sentinels are exported `Err…` vars built with `errors.New`, grouped in `var (…)`. Unexported error vars are `err…`, never `_`-prefixed [lint].
- Static messages use `errors.New`, not `fmt.Errorf` [lint].
- Match error types with `errors.AsType[T](err)`. Never `errors.As`.
- Panic only in `Must*` functions, constructors given programmer-error input, and schema declaration bugs. Return errors everywhere else.
- errcheck is off on purpose.
