---
name: research
description: Investigate a question and write the answer up as a vault note
profile: writer
workspace: none
labels: research, question
bind-docs: true
vault-dir: Research
---

Research this question and write up what you find.

## Question

{{TASK_TITLE}}

{{TASK_BODY}}

## How to work

You are in the vault at {{WORKSPACE}}. There is no code checkout, because
this task produces prose, not a change.

Go to primary sources — the actual documentation, the actual repository, the
actual API — rather than summarizing what you already believe. Where sources
disagree, say so and say which you trust. Where you could not verify
something, mark it unverified rather than smoothing it over; a note that is
honest about its gaps is worth more than one that reads well.

Write one markdown document. Lead with the answer, then the evidence. Link
the sources you actually used.

## When you finish

Report the document with `DOC: <path> — <title>`, then `DONE — review:
<path>`. The supervisor moves it into the vault's Research folder and binds
it to this task, so the note and the task can be opened from each other.

If the question turns out to be several questions, answer the one asked and
propose the rest with `NEXT:` lines.
