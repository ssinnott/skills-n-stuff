# wf

A workflow CLI that runs agent work off a queue.

`wf` reads ready work from a tracker, leases it, gives it an isolated
worktree, runs a coding agent under a canned workflow, parses the agent's
outcome verbs, and writes the results back as comments, links, and
evidence-backed closes. Documents the agent produces land in your Obsidian
vault, linked to the task in both directions.

The interactive agent session is a client of this CLI, not its host —
restarting your TUI means nothing to running work.

kata is the first queue backend, pi the first runner, git worktrees the first
workspace. See [DESIGN.md](DESIGN.md) for why the seams sit where they do,
and what was rejected on the way.

## Build

```sh
go build -o wf ./cmd/wf
go test ./...
```

No dependencies beyond the standard library.

## Usage

```sh
wf ready --limit 10          # actionable work, top of queue first
wf show abc4                 # task, workflow, lease, session, bound note
wf workflows                 # canned workflows that are loaded
wf run --once                # dispatch the top claimable task
wf run --ref abc4            # dispatch one specific task
wf run --max 3               # drain the queue, three agents at a time
wf escalations               # what needs a human
wf attach abc4               # reopen the pi session that ran this task
wf bind abc4 notes/plan.md   # bind a task to a note by hand
wf ui abc4                   # deep link into kata's web UI
```

## Config

`~/.wf/config.json`, or `--config`:

```json
{
  "actor": "wf-laptop",
  "repo": "~/code/app",
  "vault": "~/vault",
  "worktreeRoot": "~/.wf/worktrees",
  "workflowDir": "~/.wf/workflows",
  "profiles": { "coding": "~/.pi/profiles/coding", "writer": "~/.pi/profiles/writer" },
  "defaultProfile": "coding",
  "maxConcurrent": 3,
  "leaseTTLSeconds": 900
}
```

`KATA_BIN` and `PI_BIN` override binaries that are off `PATH` — they usually
are under a launchd or systemd unit.

## Canned workflows

A workflow is a markdown file in `workflowDir`: flat frontmatter, then the
prompt. Copy [`workflows/`](workflows/) to `~/.wf/workflows` to start.

```markdown
---
name: research
description: Investigate a question and write it up as a vault note
profile: writer          # → PI_CODING_AGENT_DIR via config.profiles
workspace: none          # or worktree (default)
labels: research         # tasks with this label select this workflow
bind-docs: true          # DOC artifacts move into the vault
vault-dir: Research      # …to here
resources: /vault/templates/brief.md
---

Research {{TASK_TITLE}} and write it up.

{{TASK_BODY}}
```

Placeholders: `{{TASK_REF}}`, `{{TASK_ID}}`, `{{TASK_TITLE}}`,
`{{TASK_BODY}}`, `{{WORKSPACE}}`, `{{RESOURCES}}`, `{{WORKFLOW}}`.

A workflow is dispatch wiring, not expertise — which profile, which prompt,
which resources, where artifacts land. The judgment lives in the skills the
profile loads. Selection is `wf.workflow` metadata first, then label match; a
task naming a workflow that is not loaded escalates rather than running under
a default.

## How the pieces bind

**Task ↔ session.** Every run records `pi.session` (the session file path),
`pi.session_id`, `pi.workspace`, and appends to a `pi.session_history` array.
These are written *at spawn*, before the agent produces anything, so a crashed
or hung run is still attachable — those are the runs you most need to read.
`wf attach` resolves them and execs `pi --session <path>`.

**Task ↔ note.** The note's frontmatter carries `kata-issue: <ULID>`; the task
carries `obsidian.note: <path>`. Workflows with `bind-docs` do this
automatically for every document produced. Nothing is mirrored: titles and
status live in the tracker, prose lives in the note, and the only shared state
is the id pair.

**Agent ↔ tracker.** The seed prompt names the agent's issue, so it can read
context and comment progress itself. But claim, close and lease transitions
belong to `wf` alone — two writers on a terminal transition produces
double-closes and dropped evidence.

## What happens to a run

| Outcome | Result |
| :-- | :-- |
| Reported `DONE` **with evidence** | Task closed with PR/commit/document evidence; worktree disposed |
| `NEXT:` lines | Follow-on tasks created, linked to the parent, not launched |
| `ISSUE:` lines | Recorded on the task; never gates the close |
| `DOC:` lines | Moved into the vault and bound, if the workflow says so |
| `DONE` with **no** evidence | Escalated — see below |
| No `DONE` | Escalated: flagged `needs-human`, worktree **kept**, transcript excerpted onto the task |
| Agent crashed | Same escalation path, with the failure output |

Two rules make it safe to leave running.

**Silence is never success.** A run that stopped talking does not close a task.

**A completion with nothing to show for it is not a completion.** An agent
that reports `DONE` having produced no PR, commit, document, or test either
did nothing or forgot to say what it did, and a human should look before the
ledger records it as finished. A filed `ISSUE:` does not count — it says work
moved elsewhere, not that this task's work exists.

kata enforces the same rule independently, refusing an evidence-free close.
wf does not synthesize evidence to get past it: invented evidence is precisely
what would make a closed task worthless.

## Testing

```sh
go test ./...              # everything, integration included when kata is present
go test -race ./...
```

Unit tests run anywhere. The integration tests need the real `kata` binary on
`PATH` (or `KATA_BIN` set) and skip cleanly without it:

- **`internal/kata`** drives a live daemon in a throwaway `KATA_HOME`:
  create/get/ready, metadata round-trips, lease survival through kata's JSON,
  claim conflicts, release, close with multiple pieces of evidence,
  escalation queries, idempotent follow-on creates.
- **`internal/supervisor`** runs the whole loop against real kata, real git
  worktrees, and a stub agent — closing a real issue, escalating and keeping
  the worktree, moving a produced document into a vault and binding it both
  ways, spawning a linked follow-on, and four concurrent runs.

The agent is a stub rather than pi itself because pi needs a provider key and
would make the tests non-deterministic. `BuildArgs` in `internal/runner/pi.go`
is therefore the one assumption the suite does not cover: it takes `-p` as
positional and `--session` as accepting a not-yet-existing path.

Writing these tests against kata v0.16.0 contradicted its published reference
in five places — `--if-unowned` does not exist, `close` takes no idempotency
key, `--pr` is single-valued, releasing ownership is `edit --owner ""`, and
issues carry both an integer `id` and the `uid` ULID you actually want. All
five are fixed and pinned. `NormalizeIssue` remains the single point of
contact with kata's wire format.

## Layout

```
cmd/wf/                command dispatch
internal/wf/           task model, outcome protocol, leases, sessions, apply
internal/supervisor/   the dispatch loop
internal/workflow/     canned workflows
internal/kata/         kata queue backend (the only wire-format assumptions)
internal/runner/       pi runner
internal/workspace/    git worktrees
internal/note/         Obsidian frontmatter binding
internal/config/       settings
workflows/             example workflows to copy into ~/.wf/workflows
```

Leases and the work-state vocabulary live in `internal/wf`, deliberately
above the backend interface: no tracker in scope implements either, so
pushing them down would mean writing them once per adapter.
