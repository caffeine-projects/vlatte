---
name: plan-change
description: 'Plan a change to vlatte before writing code: a feature, a fix, a refactor, a new package, matcher, reply, option or API. Use at the start of every standard change, as AGENTS.md sizes it; a small change does not load it. Builds the plan from the code and AGENTS.md and CONVENTIONS.md, never from the docs; names the files and their tests, the existing code reused and the skills to load; and makes it ready for a cheaper model to carry out.'
---

# Plan a change

Use before the first edit of a standard change (`AGENTS.md` § Size the change). A small change states its plan in chat and does not load this skill.

The rules live in `AGENTS.md` and `CONVENTIONS.md`. This skill copies none of them: it makes you open the right ones and put their words where you decide.

## Sources

Design from the code: exports, callers, tests, `.golangci.yml` (Working rules: Facts from the code). Read the docs that rule excludes only to find what the change makes wrong; each page it does becomes a step under `## Docs to update`.

## What a plan holds

A plan is done when it holds each item below that applies to the change. They are the plan's success criteria, not a script to recite.

1. **Restate the request** in the user's words. List what it leaves open; do not fill the gaps with requirements of your own.
2. **Read before you design.** Read the exports, immediate callers and shared utilities of the code you will touch. List the existing types, helpers and patterns the change reuses. A new type, helper or option states why none of them fits (Working rules: Reuse first).
3. **Name the files.** Every file the change creates, edits or deletes, marked `Create`, `Modify` or `Delete`, and the test file of each. `CONVENTIONS.md` § Tests decides where each test goes; quote it in step 4.
4. **Quote the rules.** For each kind of file in step 3, open the section the `AGENTS.md` Rule map names, and quote the lines that decide it. Quote; do not paraphrase.
5. **Load the skills** the `AGENTS.md` Skills table selects for this work, before designing. Write down which, and what each changed in the plan: for `use-modern-go`, the guidelines its `list` step returned that apply.
6. **Ask only what is left.** Ask only what the code and the rule files cannot answer. Each option states a choice, never a requirement the user did not give; recommend one (Working rules: Look, then ask).
7. **Cut what serves nothing.** Tie every field, branch, option and file to a requirement from step 1, and remove the rest (Working rules: Minimum change).
8. **Make it implementation-ready.** A cheaper model must be able to carry out the plan without taking a single design decision: ordered steps, exact paths, every new or changed signature written out, each test named with what it asserts, and the verification commands. Anything still to decide goes back to step 6.
9. **Refine with EGM** (below) when the change touches more than one file besides its tests, or an exported API.
10. **Assign the steps.** `AGENTS.md` § Use the cheapest thing that gets it right says which steps a cheaper model carries out and what its brief holds. Mark each step with its role (`scout`, `implementer`, or you); a brief names `AGENTS.md` and the sections from step 4.
11. **Plan the review.** After implementing, run `review-change` and the checks `CONVENTIONS.md` § Checks names for the change.

## Refine with EGM

The EGM plugin (Elephant-Goldfish Model) reviews a written plan with sub-agents that have none of your context. It is installed per user: check that its agents (`egm:egm-implementation-readiness`, …) exist before relying on it.

1. Write the plan to a file under `docs/.ai/` or in your tool's plan directory, which keeps it out of the diff. Write no path in it as a Markdown link: EGM hands every linked file to its reviewers as reading.
2. Start these reviewers on it in parallel:
   - always `egm:egm-implementation-readiness` (can another model build it as written?) and `egm:egm-codebase-details` (do its claims match the code?);
   - also, for a new exported API, option or package, `egm:egm-zoom-out` (is it the simplest design, does it reuse what exists?) and `egm:egm-design` (does the architecture hold?), pointed at `## Context`, `## Reuse` and `## Files and tests` only.

   They know nothing of this repository. Brief each with `Mode: REVIEW`, the plan's absolute path, the Target Standard (the absolute path of this file and the steps of § What a plan holds that apply: 8 for the first two, 2, 3 and 7 for `egm:egm-zoom-out` and `egm:egm-design`), and: "Read the root `AGENTS.md` (absolute path) and the rule sections the plan quotes. Check every claim against the code. Never use `docs/`, a `README.md` or `docs/.ai/` as evidence: they may be stale."
3. Fix the plan for each finding, or tell the user why you reject it.
4. Repeat until a round reports no Blocker or Major finding. After two rounds that still do, stop and ask (Working rules: Two attempts).

When the plugin is not installed and step 9 applies, the plan still meets step 8, and says the EGM review was skipped (Working rules: Report what is true).

## Plan template

```md
## Context

## Reuse

## Files and tests

## Rules

## Skills

## Steps

## Docs to update

## Verification
```

## Example

The request: "Add a reply that serves its replies in turn and starts over after the last one."

Wrong: a new `cycleReplier` with its own mutex and counter, copied from `sequenceReplier`; its tests in `reply_test.go`; a mock file key nobody planned; a concurrency test that sleeps; a step that says "add the option". Right:

- **Context:** open question: what wins when `OnSequenceEnded` is also set. Ask, recommending one.
- **Reuse:** `SequentialReply` (`mockhttp/reply_seq.go`) already serves replies in order, one per request, with a place in the sequence for each mock; starting over is one more case of `sequenceReplier.next`. `newReplier`, `owned` and `closeAll` (`mockhttp/reply.go`) already make and release the replies it holds. No new replier type.
- **Files and tests:**
  - Modify `mockhttp/reply_seq.go`: it gains the option; `mockhttp/reply_seq_test.go` holds its tests.
  - Modify `mockhttp/mock_schema.go`, where mock files declare sequences under `response_sequence`, so the new key loads; `SequentialReply.Describe` writes it. Tests in `mockhttp/mock_schema_test.go`. `mockhttp/admin_store.go` needs no change: it refuses a mock whose description does not load back, so `mockhttp/admin_store_test.go` gains a round-trip test of a mock using the option.
  - Modify the JSON schema of mock files, `mockhttp/schema/http.schema.json`: it gains the key; `mockhttp/mock_schema_test.go` already fails until it does.
- **Rules:**
  - CONVENTIONS § Tests: "A file has one test file, beside it and named after it";
  - CONVENTIONS § Time in tests: "That something happened: the code under test signals a channel or a `sync.WaitGroup`, and the test waits on it";
  - CONVENTIONS § Documentation: "Every API a doc comment, an `Example` or the README shows exists today, with the signature shown".
- **Skills:**
  - `use-modern-go`: `list` on `reply_seq.go`; `sync_waitgroup_go` applies to the concurrency test;
  - `uber-go-style`: `references/goroutine-exit.md` for that test, which must wait for every goroutine it starts.
- **Steps:** each names the file, the signature and its role, such as "`implementer`: add `func (r *SequentialReply) Loop() *SequentialReply` to `mockhttp/reply_seq.go`", and each test with its assertion, such as "the fourth request to a three-reply loop gets the first reply".
- **Docs to update:** the pages `git grep -n 'response_sequence' -- 'docs/http/*.md'` finds.
