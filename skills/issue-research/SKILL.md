---
name: issue-research
description: Research a problem — a bug report, build failure, flaky test, dependency alert, support question — down to a well-evidenced issue filed on the tracker, reporting the pi-tasks outcome protocol at the end. Use when a task names /issue-research, or asks to investigate something and file or write up an issue rather than fix it.
---

# Issue research

The deliverable is a filed issue someone can act on without redoing the
research — not a fix. Resist fixing: if the fix became obvious along the
way, say so in the issue and chain it as a follow-up task instead.

Evidence first. Reproduce the problem or pin it to a record of it — the
failing command, the CI log, the version range — and keep the exact
commands and output. Then locate the suspect area in the code: file
paths, and the mechanism as far as you can actually demonstrate it.
Keep what you verified and what you merely suspect clearly separated;
a wrong confident claim costs the next person more than an honest gap.

Before filing, search the tracker for duplicates
(`gh issue list --search`). A duplicate gets a comment adding your
evidence, not a second issue.

File with `gh`: a title stating the symptom in effect terms (what
breaks for whom, not the internal mechanism), a body with the
reproduction and evidence, the suspected cause with paths, and the
impact. Link the log or run you worked from.

End your final message with the outcome protocol:

```
ISSUE: <url> — <title>     (one line per issue filed or commented on)
DONE
```

Issues don't gate the task — filing was the work. If the fix is worth
doing, add `NEXT: Fix <symptom> via /plan-to-pr — <issue url>` so the
follow-up task carries the artifact. `BLOCKED` plus the reason if you
cannot gather real evidence. Never invent URLs or file issues on
repositories the task didn't point you at.
