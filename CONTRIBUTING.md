# Contributing

Thanks for helping with VLatte. This page covers what you need to get a change merged.

- Coding conventions: [`CONVENTIONS.md`](CONVENTIONS.md)
- Rules for AI coding tools: [`AGENTS.md`](AGENTS.md)
- Security issues: [`SECURITY.md`](SECURITY.md). Never in a public issue.
- User documentation: [vlatte.dev](https://vlatte.dev/)

## Prerequisites

- Go 1.27 or later. CI tests the latest Go and the minimum in `go.mod`.
- [golangci-lint](https://golangci-lint.run/) v2.14.0, for `make lint`.
- Node.js 26, the current LTS, only to work on the docs site in `docs/`.

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

To check documentation links, install [Lychee](https://lychee.cli.rs/) v0.24.2 (the version the pinned `lychee-action` installs), then run:

```sh
make docs-build
make docs-check-links
make docs-check-external-links
```

Internal checks run offline on the built site and repository Markdown, including heading anchors, images and downloads. Links to `https://vlatte.dev/` resolve against the local build. CI runs these checks on pull requests and pushes to `main` before publishing the site.

External checks run weekly and on manual runs of the **External Links** workflow, so third-party outages do not block pull requests. They check HTTP and HTTPS links without validating remote heading anchors. Both checks use `lychee.toml`; code examples and private network addresses are excluded. Any exception for a public URL must name the specific URL and explain why it cannot be checked.

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

The release notes list each `feat`, `fix` and `perf` commit by its first line. Mark a breaking change with `!` after the type or scope, as in `feat(admin)!: rewrite the admin API`: the notes list it under Breaking changes, whatever its type.

## Pull requests

- One concern per pull request.
- Link the issue it closes, if there is one.
- Add or update tests for any change in behavior, and update the docs when usage changes.
- All checks pass before you ask for review.

You may use AI tools. You own every line you submit and must be able to explain it. The tools follow [`AGENTS.md`](AGENTS.md).

## Releasing

For maintainers. Pushing a tag that starts with `v` runs [`.github/workflows/release.yml`](.github/workflows/release.yml). A tag with a suffix, such as `v0.2.0-rc.1`, is a prerelease, and Homebrew does not get it.

1. For a stable release, bump the version the docs pin, and merge that first:

   ```sh
   grep -rnE 'vlatte:v[0-9]|VERSION=v[0-9]' README.md docs --exclude-dir=node_modules
   ```

2. Sign the tag and push it. The first `-m` is the tag's subject. Each later `-m` is a paragraph of the highlights that open the release notes. Never push a lightweight tag: the notes would open with the tagged commit's body instead.

   ```sh
   git tag -s v0.2.0 -m v0.2.0 -m "Highlights of the release."
   git push origin v0.2.0
   ```

3. Approve the `release` environment when its jobs ask.
4. Check the release notes and, for a stable release, the new commit in [caffeine-projects/homebrew-tap](https://github.com/caffeine-projects/homebrew-tap).

For a stable release, the `goreleaser` job stops before it creates the GitHub release when `HOMEBREW_TAP_GITHUB_TOKEN` has expired. Replace the token in the `release` environment and re-run the failed jobs.

## Issues

Report bugs and propose features with the [issue forms](https://github.com/caffeine-projects/vlatte/issues/new/choose). Include the VLatte version and a minimal reproduction for bugs.
