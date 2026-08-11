---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog — the diff already records every change.
It exists for a reviewer deciding in under a minute: what is this trying to
do, is it safe, and where should I look hard?

Sort the diff into three buckets: the point (the change the PR exists to
make), supporting changes, and noise (lockfiles, formatting, generated
files, mass renames). Write almost entirely about the point; supporting
changes get a line, noise one aggregate clause or nothing.

A mechanical diff can hide one real behavioral change — a rename that also
changes a default. That buried change IS the point: put it in the title or
first sentence, plainly, never as a bolded warning. A change threading
several areas (route → service → queue → worker) gets its path in one line —
name the areas, say what travels between them; that map beats any file list.

Fill in `assets/template.md`: an imperative title (≤ 70 chars) naming the
point, a one-sentence lead (why, then what), three to six single-fact
bullets (the behavior change, a judgment call worth questioning, the noise
note, where to look), and a short Testing section saying what the new tests
cover.

Describe effect, not mechanism — "retries a few times with growing pauses,"
never constants, status codes ("rate limits," not 429), or identifiers like
ECONNRESET; the diff already holds those. No worked examples. Never
enumerate files — more than about three paths is the changelog trap. Say
only what the diff shows: never invent testing, author intent, or scope.

Keep the body under 75 words for most PRs, near 120 for a several-idea
change — if over, drop the weakest bullet, then anything the title already
says. Worked examples live in `examples/`, paired with input diffs in
`evals/fixtures/`.
