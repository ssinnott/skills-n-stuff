---
name: plan-to-pr
description: Take a task from statement to pull request through an explicit plan — worktree, plan, implement, PR — reporting the pi-tasks outcome protocol at the end. Use when a task names /plan-to-pr, or asks to plan and ship a code change to a repository as one or more pull requests.
---

# Plan to PR

The deliverable is a mergeable pull request whose plan was visible
before the code existed. Work happens in the named repository (a
`REPO:`/repo reference in the task, or ask) — never in the vault, and
never directly on the default branch.

Start with a worktree or branch off the up-to-date default branch,
named for the task. Then plan before touching files: read the code the
change touches, and write down what will change, where, and what will
prove it works. If the task calls for review of the plan itself, write
it as a document, report it for review, and wait for comments to be
resolved before implementing.

Implement on the branch. Run the repository's own checks — its tests,
lint, typecheck — not ones you invent. Commit in reviewable units with
clear subjects; if the history gets messy, reorganize it before the PR.

Open the PR with `gh`, using the pr-description skill for the body.
Independent changes get independent PRs — don't couple unrelated work
to one review.

End your final message with the outcome protocol:

```
PR: <url> — <title>        (one line per PR opened)
REPO: <absolute repo path>
DONE
```

`BLOCKED` plus the reason if you cannot get to a PR. Add one
`NEXT: <task text>` line per follow-up you found but scoped out, with
the relevant link in the text. Never invent URLs or paths.
