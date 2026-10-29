# Contributing

Thanks for helping with VLatte. This page covers what you need to get a change merged.

- Coding conventions: [`CONVENTIONS.md`](CONVENTIONS.md)
- Rules for AI coding tools: [`AGENTS.md`](AGENTS.md)
- Security issues: [`SECURITY.md`](SECURITY.md). Never in a public issue.
- User documentation: [caffeine-projects.github.io/vlatte](https://caffeine-projects.github.io/vlatte/)

## Prerequisites

- Go 1.27 or later. CI tests the latest Go and the minimum in `go.mod`.
- [golangci-lint](https://golangci-lint.run/) v2.14.0, for `make lint`.
- Node.js 22, only to work on the docs site in `docs/`.

[`mise.toml`](mise.toml) pins all three: run `mise install` if you use [mise](https://mise.jdx.dev/).

## Setup

```sh
git clone https://github.com/caffeine-projects/vlatte.git
cd vlatte
make deps
```

`make help` lists every target.

## Checks

Run these before you open a pull request, in this order. CI runs the same checks.

```sh
make fmt    # format with gci and gofumpt
make tidy   # tidy go.mod and go.sum
make lint   # golangci-lint
make test   # every test, with the race detector
```

Also useful: `make fuzz`, `make bench` and `make vuln`.

The Go checks in CI skip changes that touch only `*.md` files or `docs/`. For the docs site, `make docs-dev` serves it with live reload and `make docs-build` builds it.

## What reviewers look for

[`CONVENTIONS.md`](CONVENTIONS.md) has the full rules. These are the ones most often missed:

- **Tests check behavior.** Call the code the way a user does and assert the exact result. A test that still passes with the feature broken is wrong.
- **Bug fixes start with a failing test.** Write the test that reproduces the bug, see it fail, then fix it.
- **Tests do not sleep.** Wait on a channel or use `testing/synctest`, not `time.Sleep`.
- **Errors** are lowercase, start with their scope (`store: …`) and wrap with `%w`.
- **Public APIs have doc comments** written for the caller, and the docs site changes with the behavior it describes.
- **GitHub Actions are pinned** to a full commit SHA.

## Commits

The commit that lands on `main` follows [Conventional Commits](https://www.conventionalcommits.org/):

```text
<type>(<optional scope>): <description>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `perf`. Keep the description under 72 characters, in the imperative mood, with no trailing period.

```text
feat(verify): add a request verification API
fix: panic when Jitter deviation is over 200
docs: document the exported API for pkg.go.dev
```

## Pull requests

- One concern per pull request.
- Link the issue it closes, if there is one.
- Add or update tests for any change in behavior, and update the docs when usage changes.
- All checks pass before you ask for review.

You may use AI tools. You own every line you submit and must be able to explain it. The tools follow [`AGENTS.md`](AGENTS.md).

## Issues

Report bugs and propose features with the [issue forms](https://github.com/caffeine-projects/vlatte/issues/new/choose). Include the VLatte version and a minimal reproduction for bugs.
