---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog — the diff already records every change.
It exists for a reviewer deciding in under a minute: what is this trying to
do, is it safe, and where should I look hard?

## Process

Read the whole diff, then sort it into three buckets: the point (the change
the PR exists to make — usually one thing), supporting changes (what the
point required), and noise (lockfiles, formatting, generated files, mass
renames). Write almost entirely about the point; supporting changes get a
line at most, noise one aggregate clause or nothing.

Large mechanical diffs sometimes hide one real behavioral change — a rename
that also changes a default. That buried change IS the point: put it in the
title or first sentence, as an ordinary sentence, not a bolded warning.

When a change threads through several areas (route → service → queue →
worker), give the reader that path in one line and say what travels along
it — that map beats any file list.

## Writing it

Start from `assets/template.md`: an imperative title (≤ 70 chars) naming the
point, a one-sentence lead (why, then what), then three to six short
bullets, each a single plain fact — the behavior change, a judgment call
worth questioning, what the new tests cover, the noise note, where to look.
A PR small enough for two sentences skips the bullets. If the repository
has a PR template, apply this same judgment inside its sections.

Describe effect, not mechanism: "retries a few times with growing pauses,"
never backoff constants, status codes (say "rate limits," not 429), or
identifiers like ECONNRESET — all of that already sits in the diff, and
repeating it is noise wearing a lab coat. No worked examples; the tests
carry those. Never enumerate files — more than about three paths is the
changelog trap.

Say only what the diff shows: don't invent testing, don't guess the
author's reasons for a change, and don't overstate scope. An unexplained
change gets flagged as worth confirming, not handed a rationale.

Most PRs fit in under 75 words of body; even a several-idea change should
stay near 120 — if over, drop the weakest bullet. After drafting, cut
anything the title already says.

Unsure what good looks like? `examples/` holds finished descriptions, each
paired with its input diff in `evals/fixtures/`.
