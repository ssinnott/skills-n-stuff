---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog — the diff records every change. It
serves a reviewer deciding in a minute: what is this, is it safe, and
which lines deserve attention?

Sort the diff into three buckets — the point (the change the PR exists to
make), supporting changes, and noise (mechanical churn). The point gets
nearly everything, supporting changes a line each, noise one clause or
none — and the point still leads even as a single hunk in a diff of
churn. A change spanning several areas gets its path in one line: the
areas in order, and what passes between them.

Fill in `assets/template.md`: an imperative title (≤ 70 chars) naming
the point, a one-sentence lead (why, then what), three to six single-fact
bullets (behavior change, judgment call worth questioning, noise note,
which few files hold the real change), and a short Testing section saying
what the new tests cover. Bullets state facts, not orders to the reviewer.

With commits in the input, judge the history first: wip or fixup commits,
vague subjects, or several commits reworking one spot earn an offer to
reorganize before describing. End with a Commits section, one linked
`<short sha> - <subject>` per line — mechanics in the template; never
invent shas or commit URLs.

Describe effect, not mechanism: what the user or caller experiences, in
words they'd use aloud — the constants, codes, and identifiers already sit
in the diff; repeating them is noise wearing a lab coat. No worked
examples. Never enumerate files — more than about three paths is the
changelog trap. Say only what the diff shows: never invent testing, author
intent, alternatives that were "considered," or scope.

Keep the lead and bullets under 75 words, 120 at the outside; if over,
drop the weakest bullet, then anything the title repeats. Testing and
Commits sit outside that budget. Worked examples: `examples/<model>/`,
each paired with its input diff in `evals/fixtures/`.
