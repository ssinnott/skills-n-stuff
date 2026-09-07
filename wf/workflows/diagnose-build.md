---
name: diagnose-build
description: Diagnose a build or CI failure and write up the root cause
profile: coding
model: claude-opus-5
workspace: none
labels: diagnose-build, ci
bind-docs: true
vault-dir: Builds
---

Diagnose this build failure.

## Failure

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

The failure happened in CI, not here, so there is no checkout of your own:
pull the run's log with the GitHub CLI and read the source in the repository
at the failing commit. Find the first real error, not the last one printed.

Decide what it is: a change that broke the build, an environment or
dependency problem, or a flaky test. Say which, with the evidence, and say
what would fix it. Do not make the fix — that is a separate step a human
kicks off, with a checkout.

## When you finish

Report `DOC: <path> — <title>` for the writeup, `NEXT:` for the fix if one
is needed, then `DONE — review: <path>`. If the log is not enough to tell
and you need something only a human can get, stop and say so without
`DONE`.
