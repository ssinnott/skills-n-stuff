---
name: address-comments
description: Address review comments on the task's open pull request
profile: coding
model: claude-opus-5
workspace: worktree
labels: address-comments
---

Address the review comments on this task's pull request.

## Task

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

You are in a git worktree at {{WORKSPACE}}, on the branch the pull request
was opened from. Pull first: the branch may have moved since it was pushed.

Read every unresolved review thread on the pull request and the task's own
comments. Make the changes reviewers asked for, one commit per thread or
per closely related group, and reply on each thread with what you did. Where
you disagree with a reviewer, say why on the thread rather than silently
ignoring it or silently complying. Run the repository's checks before you
push.

## When you finish

Report `PR: <url> — <title>` for the pull request you worked on, then
`DONE`. If a comment asks for a change you cannot make without a decision
from a human — a design change, a scope change — stop and say so without
`DONE`.
