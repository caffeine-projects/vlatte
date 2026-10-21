---
name: review-change
description: "Review a change to vlatte against AGENTS.md and CONVENTIONS.md, after implementing and before reporting it done, committing or pushing. Use on your own diff or on someone else's."
---

# Review a change

Use before handing over any change (`AGENTS.md` Rule 14). It finds what the author read past: a rule broken, an existing type duplicated, a test in another file's test file.

## Fresh context

Run this in a fresh context where your tool offers one, such as a sub-agent or a new session, so the rules are read without the author's reasons for bending them. Without one, re-read the rule files before you look at the diff.

## What a review covers

A review is done when it has covered each item below, in order. They are the review's success criteria, not a script to recite.

1. **Get the diff.** Run `git fetch origin main`: a clone's `origin/main` lags until it is fetched. Then run `git merge-base HEAD origin/main`. It must print a commit. In a shallow clone it can print nothing, and an empty base makes `git diff` show only unstaged changes; deepen the clone (`git fetch --deepen=200 origin main`) and retry rather than review an empty diff. Then run `git diff <base>`, and `git status` for files not yet tracked. List the changed files.
2. **Read the rules now:** `AGENTS.md` whole, and for each changed file, the section the `AGENTS.md` Rule map names.
3. **Check each file** against its sections and `AGENTS.md` Rules 1–14.
4. **Check the design**, which a file-by-file pass misses:
   - a new type, helper or option where one already existed;
   - a field, branch, option or piece of state no requirement needs;
   - one capability's code spread over several packages, or a file's tests in another file's test file;
   - an exported signature that names a type from an `internal/` package, which callers cannot name;
   - a test that still passes with the behavior it names broken.
5. **Check the checks.** The ones `CONVENTIONS.md` § After every edit names for the diff ran, and passed.
6. **Run your tool's own review commands** where it has them, such as `/simplify` and then `/code-review` in Claude Code. On someone else's diff, run only those that change nothing, such as `/code-review` without `--fix`.
7. **Report** each finding as `file:line`, the rule quoted, and the fix, and say "no findings" only after listing the rules you checked. On your own diff, fix what you found before you report it; on someone else's, report it.
