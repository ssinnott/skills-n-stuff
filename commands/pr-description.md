---
description: Write a reviewer-focused PR description from a code change
argument-hint: [branch, commit range, or diff file — defaults to current branch vs default branch]
---

Write a pull request description for: $ARGUMENTS

If no target was given above, describe the current branch's outstanding
changes: diff against the repository's default branch (use the merge base),
including uncommitted work if present.

Follow the pr-description skill for how to write it — load that skill if it
isn't already loaded. In short: sort the diff into the point, supporting
changes, and noise; write almost entirely about the point; surface any real
behavioral change buried in mechanical churn; never enumerate files. Start
from the skill's assets/template.md and drop every optional section that
doesn't earn its place.

Output the finished title and description in a markdown code block ready to
paste into the PR, unless asked to open the PR directly.
