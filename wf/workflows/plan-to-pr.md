---
name: plan-to-pr
description: Plan a change, implement it, and open a pull request
profile: coding
model: claude-opus-5
workspace: worktree
labels: plan-to-pr, feature, bug
---

Implement this change and open a pull request for it.

## Task

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

You are in a fresh git worktree at {{WORKSPACE}}, branched for this task
alone. Nobody else is working in it.

Plan before you edit. Read enough of the surrounding code to match its
conventions rather than importing your own. Run the repository's own checks —
lint, typecheck, the tests covering what you touched — before you push, and
fix what they find. One validated push beats three speculative ones.

Keep the change minimal: what the task asks for, no more. If you find
adjacent problems worth fixing, file them with `ISSUE:` or propose them with
`NEXT:` instead of widening this change.

## When you finish

Report `REPO:` and one `PR:` line per pull request, then `DONE`. If the task
turned out to need a decision you cannot make — an ambiguous requirement, two
designs that lose different things — stop and say so without `DONE`, and the
supervisor will bring a human in.
