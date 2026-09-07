---
name: file-issue
description: File the task's bug as an issue in the upstream repository
profile: writer
model: claude-sonnet-5
workspace: none
labels: file-issue
---

File this as an issue in the repository it belongs to.

## Task

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

Read the task and its comments first: an earlier run may have left a
reproduction note and a failing test, and those are the substance of a good
issue. Check the repository's existing issues before filing, and if one
already covers this, report that one instead of opening a duplicate.

Write the issue the way the repository's own issues read: what happens,
what was expected, how to reproduce, and what version or commit. Link the
reproduction note if there is one.

## When you finish

Report `ISSUE: <url> — <title>` for the issue you filed or found, then
`DONE`. Filing the issue is this run's whole job; it is what you have to
show.
