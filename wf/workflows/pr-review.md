---
name: pr-review
description: Review the task's pull request and write the findings up
profile: coding
model: claude-opus-5
workspace: none
labels: pr-review, review
bind-docs: true
vault-dir: Reviews
---

Review this task's pull request.

## Task

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

Read the pull request's diff, description and existing comments with the
GitHub CLI. Read enough of the surrounding code to know whether the change
fits it. Look for correctness first, then for what the tests do not cover,
then for anything a maintainer would ask to change.

Write one review note: the verdict, then each finding with the file and
line it is about. Post the findings on the pull request too, as review
comments, so the author sees them where they work.

## When you finish

Report `ISSUE: <url> — <path>:<line> <finding>` for each finding you posted,
so `wf review` can seed them into the diff viewer, then `DOC: <path> —
<title>` for the note and `DONE — review: <path>`.
