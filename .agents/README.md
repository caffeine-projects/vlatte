# Agent skills

Skills that help AI coding agents contribute to vlatte. Each skill is a folder with a `SKILL.md` in the [Agent Skills format](https://agentskills.io/specification). Agents read every skill's name and description up front, and the rest only when a task needs it.

| Skill | Purpose | Origin |
|---|---|---|
| `skills/plan-change/` | Plan a change against `AGENTS.md` and `CONVENTIONS.md` before the first edit | First-party |
| `skills/review-change/` | Review a diff against `AGENTS.md` and `CONVENTIONS.md` before handing it over | First-party |
| `skills/use-modern-go/` | Modern Go idioms for the Go version in `go.mod` | Vendored |
| `skills/uber-go-style/` | Uber Go Style Guide, one file per rule | Index first-party, rules vendored |

This project's own rules are not a skill: they live in [`CONVENTIONS.md`](../CONVENTIONS.md), which every agent reads before an edit. The Skills table in [`AGENTS.md`](../AGENTS.md) says which skill to load for which work.

## First-party skills

`plan-change`, `review-change` and the `uber-go-style` index are written here. They are agent docs: "Agent docs" in [`AGENTS.md`](../AGENTS.md) governs every change to them.

## Discovery

- Codex, Cursor, GitHub Copilot and Gemini CLI read `.agents/skills/` directly.
- Claude Code reads `.claude/skills/`, a committed symlink to `../.agents/skills`.
- On Windows, symlinks need Developer Mode and `git config --global core.symlinks true` before cloning.
- If you had an untracked `.claude/skills/` folder, move it before pulling.

## Adding a skill

Create `.agents/skills/<name>/SKILL.md` with `name` (equal to the folder name) and `description` frontmatter. Keep `SKILL.md` short and put details in `references/` files, which agents read only when needed. Never create skills under `.claude/skills/`.

Then add a row to the Skills table in [`AGENTS.md`](../AGENTS.md) naming the work that loads it. `go test -run TestAgentDocs ./test` fails when a table row names no skill, a `name` differs from its folder, or `.claude/skills` stops linking here.

## Third-party skills

- Copy upstream files verbatim, with the upstream `LICENSE`.
- Don't edit vendored files. If an edit is unavoidable, state the change inside that file (Apache-2.0 §4b).
- Record every vendored skill here.

| Skill | Upstream | Commit | License | Local changes |
|---|---|---|---|---|
| `use-modern-go` | [JetBrains/go-modern-guidelines](https://github.com/JetBrains/go-modern-guidelines) `plugin/skills/use-modern-go/` | `155dc7ca10da` | Apache-2.0, Copyright 2000-2026 JetBrains s.r.o. | None. `LICENSE` copied from the repository root. |
| `uber-go-style` | [uber-go/guide](https://github.com/uber-go/guide) `src/` | `1d60a91aa5e8` | Apache-2.0 | `references/` is verbatim. `SKILL.md` is an index written for vlatte. |

`use-modern-go` installs its CLI on first use with `go install` into `~/.cache/go-modern-guidelines`, so it needs Go and network access once. `scripts/VERSION` pins the CLI version.

### Updating

```sh
git clone --depth 1 https://github.com/JetBrains/go-modern-guidelines /tmp/gmg
rsync -a --delete --exclude LICENSE /tmp/gmg/plugin/skills/use-modern-go/ .agents/skills/use-modern-go/
cp /tmp/gmg/LICENSE .agents/skills/use-modern-go/LICENSE

git clone --depth 1 https://github.com/uber-go/guide /tmp/uber-guide
rsync -a --delete /tmp/uber-guide/src/ .agents/skills/uber-go-style/references/
cp /tmp/uber-guide/LICENSE .agents/skills/uber-go-style/LICENSE
```

Then update the commit column above. `scripts/VERSION` comes with the copy, and the wrapper installs the matching CLI on next use. For `uber-go-style`, check that `SKILL.md` still lists every rule file, and update `metadata.commit`.
