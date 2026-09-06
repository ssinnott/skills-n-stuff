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
wf note sync abc4            # write the task's managed block into its note
wf note sync --all           # refresh every note the ledger already knows
wf ui abc4                   # deep link into kata's web UI
wf ui                        # the daemon's origin, for a framed UI
wf review abc4               # resolve the task's diff and open it in difit
wf review --pr <url>         # review any PR directly, no task required
wf review abc4 --stop        # stop the running viewer
wf review comment abc4       # read a pasted review prompt from stdin
wf review comment abc4 --format difit   # ingest difit's own comment store
```

Add `--json` to `ready`, `show`, `escalations`, `workflows`, `run`, `review`
and `note sync` for machine-readable output. That is the protocol both clients
speak — the pi extension and the Obsidian plugin talk to wf, never to kata
directly, so the queue backend can change without touching either.

```json
{ "tasks": [ { "id": "01M1S…", "shortId": "neck", "title": "Add the parser",
              "priority": 1, "workflow": "plan-to-pr",
              "lease": { "actor": "wf-laptop", "stale": false },
              "session": "/home/me/.wf/sessions/neck-….jsonl",
              "note": "Research/plan.md", "needsHuman": false } ] }
```

## Config

`~/.wf/config.json`, or `--config`:

```json
{
  "actor": "wf-laptop",
  "repo": "~/code/app",
  "vault": "~/vault",
  "noteDir": "Tasks",
  "worktreeRoot": "~/.wf/worktrees",
  "workflowDir": "~/.wf/workflows",
  "profiles": { "coding": "~/.pi/profiles/coding", "writer": "~/.pi/profiles/writer" },
  "defaultProfile": "coding",
  "defaultModel": "claude-sonnet-5",
  "maxConcurrent": 3,
  "leaseTTLSeconds": 900
}
```

`KATA_BIN`, `PI_BIN` and `DIFIT_BIN` override binaries that are off `PATH` —
they usually are under a launchd or systemd unit. `difitCommand` (default
`npx difit`) is a shell-style command line rather than a bare binary, since
the default itself is two words; `DIFIT_BIN` replaces the whole thing.

## Canned workflows

A workflow is a markdown file in `workflowDir`: flat frontmatter, then the
prompt. Copy [`workflows/`](workflows/) to `~/.wf/workflows` to start.

```markdown
---
name: research
description: Investigate a question and write it up as a vault note
profile: writer          # → PI_CODING_AGENT_DIR via config.profiles
model: claude-sonnet-5   # → pi's --model; empty runs config.defaultModel
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

A workflow is dispatch wiring, not expertise — which profile, which model,
which prompt, which resources, where artifacts land. The judgment lives in
the skills the profile loads. Selection is `wf.workflow` metadata first, then
label match; a task naming a workflow that is not loaded escalates rather
than running under a default.

Model lives on the workflow rather than the profile because the two vary
independently: a plan-and-implement step and a one-line triage step often
warrant different models under the very same profile. A workflow that names
none runs under `config.defaultModel`; naming neither runs under pi's own
default.

## How the pieces bind

**Task ↔ session.** Every run records `wf.session` (the session file path),
`wf.session_id`, `wf.workspace`, and appends to a `wf.session_history` array.
These are written *at spawn*, before the agent produces anything, so a crashed
or hung run is still attachable — those are the runs you most need to read.
`wf attach` resolves them and execs `pi --session <path>`.

The keys name roles, not products: *which* runner produced a session is a
value on the binding, not half of a key name, so a second runner is a new
value rather than a parallel set of keys. The `pi.*` names these replaced are
still read for one release, and never written.

**Task ↔ note.** The note's frontmatter carries `wf-task: <id>` — the durable
half, since it survives a rename in Obsidian — alongside `kata-issue: <ULID>`
naming the tracker row. The task carries the note as a `doc` binding with
`store: vault` (and `wf.doc: <path>` on the tracker, formerly `obsidian.note`,
still read). Workflows with `bind-docs` do this automatically for every
document produced. Nothing is mirrored: titles and status live in the tracker,
prose lives in the note, and the only shared state is the id pair.

`wf note sync <ref>` renders the task — its runs and what each produced — into
a managed block in that note, delimited by `%% wf:begin %%` and `%% wf:end %%`.
Everything outside the block is yours and is never touched; the block is
regenerated wholesale, so deleting it loses nothing. A task with no note gets
one under `noteDir` (default `Tasks`), named for its title and joined by
frontmatter rather than by its filename, so renaming or moving it in Obsidian
is safe — the next sync finds it again and repoints the binding. `--all`
sweeps the ledger, refreshing the notes that exist and creating none: a note
per task is a choice a human makes one task at a time. Nothing is written when
nothing changed, because the note lives in a synced vault.

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

## Review

`wf review <ref>` resolves a task to something a human can look at and opens
it in [difit](https://github.com/yoshiko-pg/difit), a local diff viewer. It
tries, in order, the first rung that matches:

1. **A PR.** If the task recorded one (`wf.pr` metadata, written when a run
   reports `PR:`), difit opens `--pr <url>` — the shipped truth once one
   exists.
2. **A live worktree.** If the run's checkout (`wf.workspace`) is still on
   disk, difit opens it directly with `--include-untracked` — this is the
   rung that matters most, because an escalated run that produced no PR is
   exactly what a human needs to look at, and its checkout is the only place
   the agent's untracked and uncommitted state still lives.
3. **A surviving branch.** If the worktree was disposed but its branch is
   still around, difit diffs it against the repo's base branch with
   `--merge-base`.
4. **A bound note.** A workflow that only produced a document has no diff at
   all; `wf review` reports the vault path and opens nothing.

`wf review --pr <url> [--repo <path>]` reviews any PR directly, bypassing
the ladder (and any task or queue lookup) entirely — it works with no kata
running at all. Use it for a PR a human opened by hand, or one that
predates `wf.pr` metadata ever being recorded. `--repo` defaults to
`config.Repo`. It participates in the same one-viewer-at-a-time lifecycle
as a task review, and the positional `<ref>` and `--pr` are mutually
exclusive — passing both is a usage error. There is no task, so nothing is
seeded.

Findings the run filed as `ISSUE:` outcomes seed the viewer as difit review
threads (`--comment`), when the issue's own title names a file and line
(`internal/foo.go:42 — nil check`) — most filed issues carry only a tracker
link and a title, not a location, and those are left unseeded rather than
guessed onto a line.

`wf review <ref> --stop` kills the running viewer. There is one review pane
at a time: a new `wf review` replaces whatever difit is already running, the
same way one worktree per task keeps two agents from fighting over a
checkout.

difit's comments live in the browser's own `localStorage`, scoped per
*origin* — `localhost:<port>` — and difit silently falls back to a
different port when its preferred one is occupied. So `wf review` remembers,
per ref, the port difit last actually bound (in `review.json`, alongside the
live-viewer record) and asks for that same port again on reopen. This makes
a task's earlier comments *likely* to still be there, not guaranteed: a
foreign process squatting the remembered port still costs that task its
comment history, and nothing can prevent that.

Feedback flows back two ways. By hand: difit keeps its comments in the
browser's own storage with no API to read them back, so open its "Copy All
Prompt" button, copy what it renders, and run `wf review comment <ref>`,
pasting into stdin. It lands on the task prefixed as human review feedback,
distinct from an agent's own comments. Or harvested: `wf review comment
<ref> --format difit` reads difit's own comment store as JSON on stdin
(what the Obsidian plugin sends after pulling it straight out of the
browser frame's `localStorage`) and renders it into the same kind of
comment, grouped by file, without a human needing to click anything.

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
- **`internal/review`** never needs difit, kata or a real repo installed:
  the target ladder is a pure function over already-resolved inputs, and
  difit's own process is stubbed behind a `Spawner` the same way pi is
  stubbed in the supervisor tests.

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

## Clients

**pi** — `extensions/wf.ts` in this repo registers `/wf` in an interactive
session: `/wf` for the queue, `/wf escalations`, `/wf show <ref>`,
`/wf run`, and `/wf attach <ref>`, which switches the live session into that
task's agent session via pi's own session replacement. The extension holds no
orchestration logic; it shells out to `wf` and renders the JSON.

**Obsidian** — [`obsidian-wf`](../obsidian-wf) adds an agent queue pane and a
framed kata UI, and needs only the `wf` binary. Selection lives on the
Obsidian side because
an embedded page cannot tell the host what you clicked; clicking a queue row
opens the bound note and points the frame at the task. Renaming a bound note
rewrites `wf.doc` through `wf bind`, so bindings survive a
reorganization.

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
internal/review/       target ladder, difit lifecycle, finding seeds
internal/config/       settings
workflows/             example workflows to copy into ~/.wf/workflows
```

Leases and the work-state vocabulary live in `internal/wf`, deliberately
above the backend interface: no tracker in scope implements either, so
pushing them down would mean writing them once per adapter.
