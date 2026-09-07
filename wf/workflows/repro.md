---
name: repro
description: Reproduce a reported bug and pin it down with a failing test or a writeup
profile: coding
model: claude-opus-5
workspace: worktree
labels: repro, bug
bind-docs: true
vault-dir: Bugs
---

Reproduce this bug.

## Report

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

You are in a fresh git worktree at {{WORKSPACE}}, branched for this task
alone. Read the report, find the code it implicates, and try to make the
failure happen on demand.

If you can reproduce it, pin it down: a failing test committed on this
branch, as small as it can be, plus a short note on what triggers it and
what you ruled out. Do not fix it — the fix is a separate step, and a human
decides whether and how.

If you cannot reproduce it, say exactly what you tried and what you saw, so
the next person does not repeat it.

## When you finish

Report `REPO:` and `DOC: <path> — <title>` for the note, then `DONE`. If
you committed a failing test, say so in the note and name the file. If the
report is too vague to act on, stop and say what is missing without `DONE`.
