# Agent instructions

Rules for changing **this** repository, for every AI tool (e.g. Claude Code, Codex, Cursor, Copilot).

Before any edit, read [`CONVENTIONS.md`](CONVENTIONS.md).

These rules apply to every task unless explicitly overridden.
Bias: caution over speed on non-trivial work. Use judgment on trivial tasks.

## Agent docs

- `CONVENTIONS.md`, this file and the first-party skills change only when the maintainer explicitly asks for that change. A request to fix or build code is not one. When your work makes a rule wrong or shows one missing, stop and propose the exact wording; do not write it.
- They hold rules: what an agent would get wrong without being told. Not design descriptions or history. A rule `make lint` checks is marked `[lint]`.
- A rule names a pattern, never a list of files or symbols that grows with the code.

## Rule map

Before you create or edit a file of a kind below, re-read its section, even if you read it at the start of the task.

| Before you create or edit… | Re-read |
|---|---|
| a Go file | CONVENTIONS.md § Code shape |
| an error, an error message or a `panic` | CONVENTIONS.md § Errors |
| a test, a test helper or a `testdata/` file | CONVENTIONS.md § Tests |
| a doc comment, a `doc.go`, an `Example` or the README | CONVENTIONS.md § Documentation |
| code that calls a third-party module | CONVENTIONS.md § Third-party APIs |
| `.github/**` | CONVENTIONS.md § Security |
| an agent doc or a skill | AGENTS.md § Agent docs |

## Skills

[`.agents/skills/`](.agents/README.md) holds skills for every agent (`.claude/skills` links to it). Load the one the table names before that work.

| Before you… | Load |
|---|---|
| plan a change to code | `plan-change` |
| hand over a change | `review-change` |
| edit a Go file; run its `list` step on the file first | `use-modern-go` |
| design, write or review Go: errors, goroutines, globals, interfaces, structs, naming, tests, options | `uber-go-style` |

When they disagree: `CONVENTIONS.md` > `use-modern-go` > `uber-go-style` > nearby code. This applies to lines you write or change. Flag older code; do not rewrite it.
If `use-modern-go` cannot run (e.g. no network on first install), say so and continue; `make lint` still flags old idioms.

`plan-change`, `review-change` and the `uber-go-style` index are written here; `use-modern-go` and the `uber-go-style` rule files are vendored. Do not edit a vendored file. Record an unavoidable patch in [`.agents/README.md`](.agents/README.md).

## Generated documentation

When generating documentation—for example from assessments, analyses, or summaries—place it under:

- **`docs/.ai`**

Examples: assessment write-ups, flow analyses, design notes. Keep human-authored docs elsewhere (e.g. `docs/` root or other subdirs); use `docs/.ai` specifically for AI-generated content so it’s easy to find and optional to commit.
Files under `.agents/` are agent tooling, not generated docs; keep them there.

## Output

Chat in short, plain English. Lead with the answer. No filler.
Keep code names, paths, and error text exact. Do not simplify those.

## Git

- Never commit or push unless asked.
- Branch: `<type>/<topic>`, a Conventional Commits type and a kebab-case topic (`fix/proxy-timeout`). Never an AI tool or model name (`claude`, `codex`, `copilot`, `cursor`, `gemini`, …).
- A branch your environment assigns that breaks the branch rule (`claude/…`, `codex/…`) is replaced with a `<type>/<topic>` one: the maintainer's standing permission.
- Conventional Commits: `<type>(<optional scope>): <description>`. Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `perf`. Description under 72 characters, imperative mood, no trailing period.
- No AI attribution in commits, pull requests, comments or reviews: no `Co-Authored-By` naming an assistant, no session link, no "Generated with" line. The contributor is the author of record.
- Never use `git stash`. If uncommitted changes exist and you need to switch state, stop and ask.
- Never revert, discard, or `git checkout --` a file outside the current task's scope, including files a tool (`make fmt`, `go fix`) changed as a side effect. That uncommitted state is other in-progress work: stop and report it.

## Rule 1 — Think Before Coding

State assumptions explicitly. If uncertain, ask rather than guess.
Present multiple interpretations when ambiguity exists.
Push back when a simpler approach exists.
Ask only what the code and these files cannot answer. An option states a choice, never a requirement the user did not give; recommend one.
Stop when confused. Name what is unclear.

## Rule 2 — Simplicity First

Minimum code that solves the problem. Nothing speculative.
No features beyond what was asked. No abstractions for single-use code.
Test: would a senior engineer say this is overcomplicated? If yes, simplify.

## Rule 3 — Surgical Changes

Touch only what you must. Clean up only your own mess.
Do not “improve” adjacent code, comments, or formatting.
Do not refactor what is not broken. Match existing style; for Go idioms see Rule 11.

## Rule 4 — Goal-Driven Execution

Define success criteria. Loop until verified.
Do not follow steps blindly. Define success and iterate.
Strong success criteria let you loop independently.
Done means the checks in `CONVENTIONS.md` § After every edit pass.

## Rule 5 — Use the model only for judgment calls

Use the model for: classification, drafting, summarization, extraction.
Do not use the model for: routing, retries, deterministic transforms.
If code can answer or produce it, code does: count, search, check, convert and generate with a command, because a command's output is exact and can be rerun, and reasoning that imitates it is neither.

## Rule 6 — Do not spiral

Do not loop on the same failure. If repeated attempts fail, stop and ask.
Say so when you are looping. Do not silently retry.

## Rule 7 — Surface conflicts, do not average them

If two patterns contradict, follow these files and the Skills precedence; where they are silent, the more recent and better-tested one.
Explain why. Flag the other for cleanup.
Do not blend conflicting patterns.
When a rule blocks what the task needs, stop and name the rule; do not work around it.

## Rule 8 — Read before you write

Before adding code, read exports, immediate callers, shared utilities.
Before adding a type, a helper or an option, find the one that exists and use it, because two names for one concept split every caller. A new one needs the reason none fits.
“Looks orthogonal” is dangerous. If unsure why code is structured a way, ask.

## Rule 9 — Tests verify intent, not just behavior

Tests must encode why behavior matters, not just what it does.
A test that cannot fail when business logic changes is wrong.
Where a test goes and how it asserts is `CONVENTIONS.md` § Tests.

## Rule 10 — Checkpoint after every significant step

Summarize what was done, what is verified, what is left.
Do not continue from a state you cannot describe back.
If you lose track, stop and restate.

## Rule 11 — Match the codebase’s conventions, even if you disagree

Conformance beats taste inside the codebase.
Conventions are what this file, `CONVENTIONS.md`, and the lint config define. Older Go idioms in nearby code are not conventions.
Code that contradicts these files is legacy, even when it sits next to yours: do not copy it, because copying is how it spreads.
If you genuinely think a convention is harmful, surface it. Do not fork silently.

## Rule 12 — Fail loud

“Completed” is wrong if anything was skipped silently.
“Tests pass” is wrong if any were skipped.
Default to surfacing uncertainty, not hiding it.

## Rule 13 — Plan against the rules

Before the first edit of a change to code, load `plan-change` and write the plan it asks for: the files and their tests, the existing code reused, the rules that decide it quoted, the skills loaded. A rule read at the start of a task fades by the time it applies; quoted into the plan, it is in front of you when you decide.

## Rule 14 — Review your own work

After each significant step, and before you hand over, run `review-change` on your whole diff, in a fresh context if your tool offers one, because an author reads past their own shortcuts. Check that it does what was asked and nothing more, follows these files, has tests that can fail, and leaves nothing behind (debug output, a TODO, a stray file). Fix what you find before you report.
