---
name: plan-change
description: 'Plan a change to vlatte before writing code: a feature, a fix, a refactor, a new package, matcher, reply, option or API. Use at the start of every implementation task. Maps the change to AGENTS.md and CONVENTIONS.md, and names the files and their tests, the existing code reused and the skills to load.'
---

# Plan a change

Use before the first edit of any change to code in this repository (`AGENTS.md` Rule 13). A one-line fix needs a short plan, not none.

The rules live in `AGENTS.md` and `CONVENTIONS.md`. This skill copies none of them: it makes you open the right ones and put their words where you decide.

## What a plan holds

A plan is done when it holds each item below that applies to the change. They are the plan's success criteria, not a script to recite.

1. **Restate the request** in the user's words. List what it leaves open; do not fill the gaps with requirements of your own.
2. **Read before you design.** Read the exports, immediate callers and shared utilities of the code you will touch. List the existing types, helpers and patterns the change reuses. A new type, helper or option states why none of them fits (Rule 8).
3. **Name the files.** Every file the change creates or edits, and the test file of each. `CONVENTIONS.md` § Tests decides where each test goes; quote it in step 4.
4. **Quote the rules.** For each kind of file in step 3, open the section the `AGENTS.md` Rule map names, and quote the lines that decide it. Quote; do not paraphrase.
5. **Load the skills** the `AGENTS.md` Skills table selects for this work, before designing. Write down which, and what each changed in the plan: for `use-modern-go`, the guidelines its `list` step returned that apply.
6. **Ask only what is left.** Ask only what the code and the rule files cannot answer. Each option states a choice, never a requirement the user did not give; recommend one (Rule 1).
7. **Cut what serves nothing.** Tie every field, branch, option and file to a requirement from step 1, and remove the rest (Rule 2).
8. **Brief your helpers.** A sub-agent you start may not see these files: tell it to read `AGENTS.md` and the sections from step 4 first.
9. **Plan the review.** After implementing, run `review-change` (Rule 14) and the checks `CONVENTIONS.md` § After every edit names for the change.

## Plan template

```md
## Context

## Reuse

## Files and tests

## Rules

## Skills

## Verification
```

## Example

The request: "Add a reply that serves its replies in turn and starts over after the last one."

Wrong: a new `cycleReplier` with its own mutex and counter, copied from `sequenceReplier`; its tests in `reply_test.go`; a mock file key nobody planned; a concurrency test that sleeps. Right:

- **Context:** open question: what wins when `OnSequenceEnded` is also set. Ask, recommending one.
- **Reuse:** `SequentialReply` (`mockhttp/reply_seq.go`) already serves replies in order, one per request, with a place in the sequence for each mock; starting over is one more case of `sequenceReplier.next`. `newReplier`, `owned` and `closeAll` (`mockhttp/reply.go`) already make and release the replies it holds. No new replier type.
- **Files and tests:**
  - `mockhttp/reply_seq.go` gains the option; `mockhttp/reply_seq_test.go` holds its tests.
  - Mock files declare sequences under `response_sequence` (`mockhttp/mock_schema.go`), `SequentialReply.Describe` writes the same key, and the admin store refuses a mock whose description does not load back (`mockhttp/admin_store.go`): the option goes in both, with tests in `mockhttp/mock_schema_test.go` and `mockhttp/admin_store_test.go`.
  - The JSON schema of mock files, `mockhttp/schema/http.schema.json`, gains the key; `mockhttp/schema_test.go` already fails until it does.
- **Rules:**
  - CONVENTIONS § Tests: "A test of one file sits beside it, named after it";
  - CONVENTIONS § Time in tests: "That something happened: the code under test signals a channel or a `sync.WaitGroup`, and the test waits on it";
  - CONVENTIONS § Documentation: "Every API a doc comment, an `Example` or the README shows exists today, with the signature shown".
- **Skills:**
  - `use-modern-go`: `list` on `reply_seq.go`; `sync_waitgroup_go` applies to the concurrency test;
  - `uber-go-style`: `references/goroutine-exit.md` for that test, which must wait for every goroutine it starts.
