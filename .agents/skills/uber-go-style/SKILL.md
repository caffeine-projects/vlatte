---
name: uber-go-style
description: Uber Go Style Guide as a rule index with one file per rule. Use when designing, writing, refactoring or reviewing Go code: errors, goroutines, globals, interfaces, structs, naming, declarations, tests, functional options. Read only the rule files the change touches.
license: Apache-2.0 (see LICENSE)
metadata:
  upstream: https://github.com/uber-go/guide
  commit: 1d60a91aa5e87d443002e23c21903c49489dbde5
---

# Uber Go Style Guide

This index was written for vlatte. `references/` is a verbatim copy of uber-go/guide `src/`.

- Precedence: `CONVENTIONS.md` > `use-modern-go` > this guide.
- Read a rule file only when the change touches that rule.
- `[lint]`: `make lint` checks the mechanical part of the rule. Fix what it flags.

Superseded by modern Go:
- references/atomic.md: use `sync/atomic` typed values (`atomic.Bool`, `atomic.Int64`, …), not `go.uber.org/atomic`.
- references/test-table.md, "Parallel Tests": since Go 1.22 each iteration has its own loop variable. No copy needed.

Repo overrides (see `CONVENTIONS.md`): `import-group.md` (three groups here) and `lint.md` (errcheck is off).

## Guidelines

- Pass interfaces as values; pointers to interfaces are almost never needed → references/interface-pointer.md
- Assert interface compliance at compile time: `var _ I = (*T)(nil)` → references/interface-compliance.md
- Value vs pointer receivers, and which ones satisfy an interface → references/interface-receiver.md
- Zero-value mutexes are valid; no mutex pointers, no embedded mutexes [lint] → references/mutex-zero-value.md
- Copy slices and maps received or returned at API boundaries → references/container-copy.md
- Release resources (files, locks) with `defer` → references/defer-clean.md
- Channels are unbuffered or size one; justify any other size → references/channel-size.md
- Start `iota` enums at one unless zero is a meaningful default → references/enum-start.md
- Use `time.Time` and `time.Duration`, also in flags, config and JSON → references/time.md
- Errors:
  - Choose `errors.New`, `fmt.Errorf`, a sentinel var or a custom type by matching needs [lint] → references/error-type.md
  - Add succinct context (`"new store: %w"`, never "failed to …"); pick `%w` or `%v` → references/error-wrap.md
  - Name sentinels `ErrX`/`errX` and types `XError`/`xError` [lint] → references/error-name.md
  - Handle each error once: wrap and return, or log and degrade, never both → references/error-once.md
- Use the comma-ok form for type assertions [lint] → references/type-assert.md
- Don't panic in production code; return errors → references/panic.md
- Avoid mutable globals; inject dependencies instead → references/global-mut.md
- Don't embed types in exported structs → references/embed-public.md
- Don't shadow predeclared names (`len`, `min`, `error`, …) [lint] → references/builtin-name.md
- Avoid `init()`; keep it deterministic and side-effect free when unavoidable [lint] → references/init.md
- Call `os.Exit` or `log.Fatal` only in `main` [lint] → references/exit-main.md
  - At most once, through a `run()` that returns errors → references/exit-once.md
- Tag every field of a marshaled struct [lint] → references/struct-tag.md
- Don't fire-and-forget goroutines; each one needs a way to stop → references/goroutine-forget.md
  - Wait for goroutines to exit (`sync.WaitGroup` or a done channel) → references/goroutine-exit.md
  - No goroutines in `init()` [lint] → references/goroutine-init.md

## Performance

- Prefer `strconv` over `fmt` for primitive/string conversion [lint] → references/strconv.md
- Convert a fixed string to `[]byte` once, outside loops → references/string-byte-slice.md
- Give `make` a capacity hint for slices and maps when the size is known [lint] → references/container-capacity.md

## Style

- Avoid overly long lines (soft limit 99 characters) → references/line-length.md
- Be consistent within a package → references/consistency.md
- Group related `import`, `const`, `var` and `type` declarations → references/decl-group.md
- Import group ordering (overridden here: three groups) → references/import-group.md
- Package names: short, lowercase, singular, never `util`/`common`/`shared`/`lib` → references/package-name.md
- MixedCaps function names; tests may use `TestX_Case` → references/function-name.md
- Alias an import only when its name differs from the path's last element or clashes → references/import-alias.md
- Order a file by call order; exported first; constructors after their type [lint] → references/function-order.md
- Handle errors and special cases first and return early → references/nest-less.md
- Replace an if/else that sets one variable with a default plus a single if → references/else-unnecessary.md
- Top-level `var` has no type unless it differs from the expression [lint] → references/global-decl.md
- Prefix unexported top-level vars and consts with `_` (errors use `err`) → references/global-name.md
- Embedded fields go first, followed by a blank line [lint] → references/struct-embed.md
- Use `:=` when assigning a value; `var` when the zero value is intended → references/var-decl.md
- `nil` is a valid empty slice: return `nil`, test with `len(s) == 0` → references/slice-nil.md
- Keep variable scope small, unless it adds nesting → references/var-scope.md
- Comment naked literal arguments (`/* isLocal */ true`) or use named types → references/param-naked.md
- Use raw string literals instead of hand-escaped strings → references/string-escape.md
- Initializing structs:
  - Use field names → references/struct-field-key.md
  - Omit zero-value fields unless they add meaning → references/struct-field-zero.md
  - Use `var s T` when every field is zero → references/struct-zero.md
  - Use `&T{}`, not `new(T)` → references/struct-pointer.md
- `make` for empty or programmatically filled maps; literals for fixed contents [lint] → references/map-init.md
- Format strings declared outside a Printf call are `const` → references/printf-const.md
- Printf-style function names end in `f` so `go vet` checks them [lint] → references/printf-name.md

## Patterns

- Table-driven tests with subtests; no branching logic in table bodies → references/test-table.md
- Functional options for optional constructor arguments → references/functional-option.md

## Linting

- Lint consistently with golangci-lint (overridden here: errcheck is off) → references/lint.md
