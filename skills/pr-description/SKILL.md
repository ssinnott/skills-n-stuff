---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog — the diff already records every change.
It exists for a reviewer deciding in under a minute: what is this trying to
do, is it safe, and which lines deserve real attention?

Sort the diff into three buckets — the point (the change the PR exists to
make), supporting changes, and noise (mechanical churn). The point gets
nearly everything; supporting changes a line each; noise one clause or none.

If a mechanical diff hides one real behavioral change, lead with that
change — title or first sentence, no special formatting. If a change spans
several areas, describe the path in one line: the areas in order, and what
passes between them.

Fill in `assets/template.md`: an imperative title (≤ 70 chars) naming the
point, a one-sentence lead (why, then what), three to six single-fact
bullets (the behavior change, a judgment call worth questioning, the noise
note, which few files hold the real change), and a short Testing section
saying what the new tests cover. Bullets state facts; they don't give the
reviewer orders.

Describe effect, not mechanism: say what the user or caller experiences, in
words they'd use aloud — the constants, codes, and internal identifiers
behind it already sit in the diff, and repeating them is noise wearing a
lab coat. No worked examples. Never enumerate files — more than about three
paths is the changelog trap. Say only what the diff shows: never invent
testing, author intent, or scope.

Keep the lead and bullets under 75 words, 120 at the outside; if over,
drop the weakest bullet, then anything the title repeats.
Testing sits outside that budget but stays to a line or two. Worked
examples: `examples/`, each paired with its diff in `evals/fixtures/`.
