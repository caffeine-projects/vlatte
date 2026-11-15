# Agent instructions

Rules for changing **this** repository, for every AI tool (Claude Code, Codex, Cursor, Copilot, …).

Before any edit, read [`CONVENTIONS.md`](CONVENTIONS.md). Every rule holds unless explicitly overridden. Where a rule gives its reason, apply it to every case the reason covers, not only to the words.

## Size the change

Decide the size before the first edit, from the request and the code. The size sets how much process the change pays for.

A change is **small** when every line below holds, and **standard** otherwise:

- It edits one Go file and its test file, or only `*.md` files.
- It adds or changes no exported API: no exported name, option, default, signature or mock file key. A fix inside one that keeps its contract does not count.
- It creates, moves or renames no file, except a test file beside its source.
- It touches nothing under `.github/`.

| Step | Small | Standard |
|---|---|---|
| Plan | A few lines in chat: the files, the test, the rule lines that decide it | Load `plan-change` and write the plan it asks for |
| Checks | `CONVENTIONS.md` § Checks | The same |
| Review | Read your own diff against the Rule map sections and the Working rules, and for anything left behind (debug output, a TODO, a stray file) | Run `review-change`: where your tool has sub-agents, one with a fresh context reviews the whole diff |
| Report | What changed, what you ran and its result | The same, and what each review round found |

- A small change that stops fitting a line above is standard from there: stop and plan before the next edit.
- When unsure, it is standard.
- A change to an agent doc, a skill or an agent definition runs `review-change` whatever its size: a wrong line there misleads every later session.

## Working rules

- **Look, then ask.** Ask only what neither the code nor these files answer: what the request leaves open, or why code you must change is built the way it is, when the change depends on the answer. Ask before the first edit when you can see the question then. Never guess a requirement. An option you offer states a choice, never a requirement the user did not give; recommend one. State any other assumption you went ahead on.
- **Minimum change.** Build what was asked and nothing beside it: no extra option, no abstraction for a single use, no refactor, reformat or comment fix in code the task does not need. Clean up only what your own change left behind. When a simpler approach than the one asked for exists, say so before you build.
- **Reuse first.** Before adding code, read the exports, callers and shared utilities around it. Before adding a type, a helper or an option, use the one that exists, because two names for one concept split every caller. A new one states why none fits.
- **Facts from the code.** Take an API shape, a constraint or a design decision from the code and these files, never from `docs/`, a `README.md` other than `.agents/README.md`, or `docs/.ai/`: they may describe the code as it once was.
- **Legacy is not precedent.** Conventions are what these files and the lint config define, not older idioms in nearby code. Code that contradicts them is legacy, even next to yours: do not copy it, because copying is how it spreads. Flag it; do not rewrite it. If you think a convention is harmful, say so; do not fork silently.
- **Conflicts.** Where two patterns contradict, follow these files and the Skills precedence; where they are silent, the more recent and better-tested one. Say which you followed and flag the other; never blend them. When a rule blocks what the task needs, stop and name the rule; do not work around it.
- **Two attempts.** After two failed attempts at the same failure, stop, say what you tried and ask. Never retry silently.
- **Report what is true.** "Done" means the checks ran and you saw them pass. Name every check, test or step you skipped and why: "tests pass" is wrong if any were skipped. A sub-agent's report is a claim until you have read its diff or the command output it returns.

## Use the cheapest thing that gets it right

Work goes to the first of these that does it right:

1. **A command**, when code can answer or produce it: count, search, check, convert, generate. A command's output is exact and can be rerun, and reasoning that imitates it is neither.
2. **A cheaper model**, in a sub-agent, for the work the table gives it.
3. **Your own model**, for everything else.

| Work | Model | Role |
|---|---|---|
| Find where something is, list callers, read a log, run the checks and report each command's exit status and what failed | the smallest your tool offers | `scout` |
| Carry out the steps of a finished plan, edit docs only, rename across callers, apply one decided pattern to many files | a mid-tier one | `implementer` |
| Size and plan a change, design an exported API or a type, a bug of unknown cause, an agent doc, a skill or an agent definition, the review | yours, never a cheaper one | `reviewer` for the review; the rest stays with you |

- The last row wins over the two above it.
- An agent definition is a file your tool reads to start a sub-agent in a set role, with its own instructions, tools and model. Where your tool has one for the role, start it: it pins the model. [`.agents/README.md`](.agents/README.md) lists the tools that have them. Otherwise choose the sub-agent's model yourself; where your tool cannot, end a standard plan by telling the user which steps a cheaper model can carry out.
- A cheaper model takes no design decision. Its brief says to stop and report where a step leaves one open; you decide, then send it back. One that fails twice at a step hands the step back to you.
- A sub-agent knows only its brief. Give it the goal, the exact files, the plan steps that are its own, the checks to run and what to return. One that edits files also gets the sections to read first: this file whole and the ones the Rule map names for its files.
- You stay the author. Read the diff a sub-agent returns before you build on it, and size, check and review the whole change as your own.
- Do not delegate what a few tool calls with short output finish: the brief costs more than the work. A search or a check run is delegated for the output it keeps out of your context.
- Handed a finished plan to carry out, as a sub-agent or because the user named the steps, you are that cheaper model: carry it out as written. Sizing, planning and `review-change` stay with whoever handed it over.

## Rule map

Before the first file of a kind below that you create or edit in a task, re-read its section, even if it is already in your context: a rule read at the start of a task fades by the time it applies.

| Before you create or edit… | Re-read |
|---|---|
| a Go file | CONVENTIONS.md § Code shape |
| an error, an error message or a `panic` | CONVENTIONS.md § Errors |
| a test, a test helper or a `testdata/` file | CONVENTIONS.md § Tests |
| a doc comment, a `doc.go`, an `Example` or the README | CONVENTIONS.md § Documentation |
| code that calls a third-party module | CONVENTIONS.md § Third-party APIs |
| `.github/**` | CONVENTIONS.md § Security |
| an agent doc, a skill or an agent definition | AGENTS.md § Agent docs |

## Skills

[`.agents/skills/`](.agents/README.md) holds skills for every agent. Load the one the table names before that work.

| Before you… | Load |
|---|---|
| plan a standard change | `plan-change` |
| hand over a standard change or a change to an agent doc, a skill or an agent definition; review a diff when asked | `review-change` |
| edit a Go file; run its `list` step on the file first | `use-modern-go` |
| design, write or review Go: errors, goroutines, globals, interfaces, structs, naming, tests, options | `uber-go-style` |

- When they disagree: `CONVENTIONS.md` > `use-modern-go` > `uber-go-style` > nearby code.
- If `use-modern-go` cannot run (e.g. no network on first install), say so and continue; `make lint` still flags old idioms.
- `plan-change`, `review-change` and the `uber-go-style` index are written here; the rest is vendored. Do not edit a vendored file. For an unavoidable patch, follow "Third-party skills" in [`.agents/README.md`](.agents/README.md).

## Agent docs

- `CONVENTIONS.md`, this file, `.agents/README.md`, the skills written here, and the tool-specific files `.agents/README.md` lists change only when the maintainer explicitly asks for that change. A request to fix or build code is not one. When your work makes a rule wrong or shows one missing, stop and propose the exact wording; do not write it.
- They hold rules: what an agent would get wrong without being told. Not design descriptions or history. A rule `make lint` checks is marked `[lint]`.
- A rule names a pattern, never a list of files or symbols that grows with the code.

## Output

- Chat in short, plain English. Lead with the answer. No filler.
- Keep code names, paths, and error text exact. Do not simplify those.
