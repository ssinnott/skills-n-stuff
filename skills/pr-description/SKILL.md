---
name: pr-description
description: Write reviewer-friendly pull request descriptions from a code change. Use whenever asked to draft, write, or improve a PR description, PR body, PR summary, or merge-request description — whether the input is a diff, a branch, staged changes, or a commit range — and even when the user just says "open a PR for this" or "describe these changes."
---

# PR Descriptions

A PR description is not a changelog. The diff already documents every change,
line by line — repeating it in prose helps no one. The description exists for a
reviewer deciding three things in under a minute: what is this PR trying to do,
is it safe, and where should I look hard? Write for that reader: a teammate
skimming twenty PRs, not an auditor reconstructing the work.

## Process

Read the whole diff first, then sort every change into three buckets:

1. **The point** — the change the PR exists to make. Usually one thing,
   occasionally two. If you can't name it in a sentence, keep reading until
   you can.
2. **Supporting changes** — things required to make the point work: test
   updates, refactors that enable it, config plumbing.
3. **Noise** — mechanical churn: lockfiles, formatting-only edits, generated
   files, version bumps, mass renames, import reordering.

Then write the description almost entirely about bucket 1. Supporting changes
get at most one line each, and only when a reviewer would otherwise be
confused to see them in the diff. Noise gets one aggregate clause at most
("also regenerates the lockfile") — or, usually, nothing.

**Watch for the buried lede.** Large mechanical diffs sometimes hide one real
behavioral change (a renamed API that also changes a default, a formatting
pass that also fixes a bug). That buried change IS the point — or at least a
point. Surfacing it prominently is the single most valuable thing the
description can do, because it's exactly what a skimming reviewer will miss.

## What to say about the point

Describe behavior, not implementation. Say what changes for users or callers:
what was broken and how it manifested, what's now possible, what's different
at the boundary. Mention implementation only where a reviewer would otherwise
be confused, or where you made a judgment call they might reasonably question
(a chosen timeout, a compatibility shim, an approach you rejected). Those
judgment calls are worth a "Notable decisions" note — they're what review
discussion is actually for.

Never enumerate files. If the draft references more than about three file
paths, that's the changelog trap — delete the list and say what the changes
accomplish instead.

## Format

- **Title**: imperative mood, ≤ 70 characters, names the point ("Add retry
  with backoff to API client", not "Updates to client code").
- **Length**: scale with the conceptual size of the change, not the diff
  size. A 40-file rename plus one behavior tweak is a small PR conceptually —
  it deserves a short description. Most PRs need under 150 words; going long
  requires the change to genuinely carry multiple ideas.
- **Structure**: start from the fill-in template at `assets/template.md` —
  a 2–4 sentence summary (why, then what), then only the optional sections
  that earn their place:
  - **Notable decisions** — judgment calls a reviewer might question
  - **Where to look** — the files or areas that deserve careful review, which
    is especially valuable when most of the diff is noise
  - **Testing** — how the change was verified, if known
- If the repository has a PR template, fill its sections instead — apply this
  same judgment inside each section rather than bolting the template on top.

## Worked examples

`examples/` holds finished descriptions from real runs, each pairing with the
input diff of the same name under `evals/fixtures/`. When unsure what "short,
point-first, noise compressed" looks like in practice, read the example whose
situation matches yours — feature buried in dependency churn, bug fix with a
drive-by rename, or a behavioral change hidden inside a mass rename.

## Tone

Plain language. Use a technical term only when it's the precise name for the
thing; never stack jargon to sound thorough. Thoroughness lives in the diff —
the description's job is clarity.
