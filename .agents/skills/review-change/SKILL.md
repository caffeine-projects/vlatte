---
name: review-change
description: "Review a standard change to vlatte, a change to its agent docs, skills or agent definitions, or any diff you are asked to review, against AGENTS.md and CONVENTIONS.md, after implementing and before reporting it done, committing or pushing. On your own diff, a sub-agent with a fresh context runs the review and you fix what it finds; on someone else's diff, run the review yourself."
---

# Review a change

Use before handing over a standard change, or a change to an agent doc, a skill or an agent definition (`AGENTS.md` § Size the change), and whenever you are asked to review a diff. It finds what the author read past: a rule broken, an existing type duplicated, a test in another file's test file.

An author reads the rules through their own reasons for bending them. On your own diff, follow **Author**, which hands the review to a sub-agent. On someone else's diff, or as that sub-agent, follow **Reviewer**.

## Author

1. **Run the checks** `CONVENTIONS.md` § Checks names for the diff, and fix them until they pass.
2. **Run your tool's own review commands** where it has them, such as `/simplify` and then `/code-review` in Claude Code. Apply what they find, then rerun the checks. Skip them when the diff changes only tests and `*.md` files, and say so in the reviewer's brief.
3. **Start one reviewer sub-agent with a fresh context.** Where your tool has an agent definition for the `reviewer` role (`.agents/README.md`), start it; elsewhere, a new sub-agent on your own model, never a fork of your context. Its brief holds only:
   - the request and every decision the user made since, in the user's words;
   - the base commit, and the checks and review commands you ran or skipped, with their result;
   - "Load the `review-change` skill and follow its Reviewer part on this diff. Change no file. Report the findings."

   Never your plan's reasoning, why you chose a design, or why a rule does not apply: the reviewer judges the diff against the rules, not against your argument.
4. **Fix every finding**, test-first for a bug (`CONVENTIONS.md` § Tests), and rerun the checks. A finding you reject goes to the user with your reason; never drop one silently.
5. **Review again** with a new sub-agent when you changed a file for a blocking or should-fix finding. After two rounds that still have such findings, stop and ask (Working rules: Two attempts).
6. **Report** what each round found and what you fixed or rejected.

A tool with no sub-agents: re-read the rule files before you look at the diff, follow Reviewer yourself, and tell the user the review ran in the author's context (Working rules: Report what is true).

## Reviewer

A review is done when it has covered each item below, in order. They are the review's success criteria, not a script to recite. Change no file.

1. **Get the diff.** Use the base commit you were given. Without one, run `git fetch origin main`: a clone's `origin/main` lags until it is fetched. Then run `git merge-base HEAD origin/main`. It must print a commit. In a shallow clone it can print nothing, and an empty base makes `git diff` show only unstaged changes; deepen the clone (`git fetch --deepen=200 origin main`) and retry rather than review an empty diff. Then run `git diff <base>`, and `git status` for files not yet tracked. List the changed files.
2. **Read the rules now:** `AGENTS.md` whole; for each changed file, the section the `AGENTS.md` Rule map names.
3. **Check the scope.** The diff does what the request asked and nothing more: no unrequested option, refactor or file.
4. **Check each file** against its sections and `AGENTS.md` § Working rules.
5. **Check the design**, which a file-by-file pass misses:
   - a new type, helper or option where one already existed;
   - a field, branch, option or piece of state no requirement needs;
   - one capability's code spread over several packages, or a file's tests in another file's test file;
   - an exported signature that names a type from an `internal/` package, which callers cannot name;
   - a test that still passes with the behavior it names broken;
   - debug output, a TODO or a stray file left behind.
6. **Check the checks.** The ones `CONVENTIONS.md` § Checks names for the diff ran, and passed. Judge them from the results you were given, and name any that is missing. Never run `make fmt`, `make tidy` or `make fix` yourself: they write files. When no Author started you, also run your tool's review commands that change nothing, such as `/code-review` without `--fix` in Claude Code.
7. **Report** each finding as `file:line`, the rule quoted, the fix, and a severity: blocking, should-fix or nit. Say "no findings" only after listing the rules you checked.
