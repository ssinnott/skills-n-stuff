---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog — the diff records every change. It
serves a reviewer deciding in a minute: what is this, is it safe, which lines matter?

Sort the diff into three buckets — the point (the change the PR exists
to make), supporting changes, and noise (mechanical churn). The point
gets nearly everything, supporting a line each, noise one clause or
none; it still leads even as a single hunk in churn. A cross-cutting
change gets its path in one line: areas in order, what travels between.

Fill in `assets/template.md`: an imperative title (≤ 70 chars) naming
the point, a one-sentence lead (why, then what), three to six single-fact
bullets (behavior change, judgment call worth questioning, noise note,
which few files hold the real change), and a short Testing section saying
what the new tests cover. Bullets state facts, not orders to the reviewer.
A repo's own PR template outranks ours — keep its headings verbatim, tick
only boxes the diff supports; the voice rules below govern every section.

With commits in the input, judge the history first (wip or fixup
commits, vague subjects, or repeated rework earn an offer to reorganize),
then end with a Commits section, one linked `<short sha> - <subject>`
per line — mechanics in the template; never invent shas or URLs.

Describe effect, not mechanism: what the user or caller experiences, in
words they'd use aloud — say "rate limits," never 429; "a bad request,"
never 400. Constants, codes, and identifiers already sit in the diff;
repeating them is noise wearing a lab coat. No worked examples;
never enumerate files (more than ~3 paths is the changelog trap). Say only
what the diff shows: never invent testing, author intent, alternatives, or scope.

Keep the lead and bullets under 75 words, 120 at the outside; if over,
drop the weakest bullet, then anything the title repeats. Testing and
Commits sit outside that budget. Worked examples: `examples/<model>/`,
each paired with its input diff in `evals/fixtures/`.
