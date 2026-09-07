# wf

A workflow CLI that runs agent workflows against tracker tasks.

A task is a unit of work — a bug, a question, a change — that lives in the
tracker from the moment it is filed until a human closes it. Along the way
you run workflows against it, one at a time, each by hand: reproduce the
bug, file it upstream, open the PR, address the review. For each run `wf`
leases the task, gives the agent a checkout (or the vault), runs it under
the workflow's prompt, parses the agent's outcome verbs, and writes what it
produced back to the task as comments and links. When the work is done,
`wf close` closes the task with the evidence those runs recorded. Documents
the agent produces land in your Obsidian vault, and the plugin renders the
task into the note it is bound to.

There is no daemon and no queue-picking: every run is a human's choice of
task and workflow. The interactive agent session is a client of this CLI,
not its host.

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
wf ready [--limit N]                                         # actionable work, top of queue first
wf show <ref>                                                # one task: its runs and what each produced
wf escalations                                               # tasks flagged needs-human
wf workflows                                                 # canned workflows loaded from the workflow dir
wf run <ref> [--workflow W] [--repo P]                       # run one workflow against a task
wf close <ref> [--message M]                                 # close a task with the evidence its runs recorded
wf attach <ref>                                              # open the task's pi session
wf bind <ref> <note.md>                                      # bind a task to an Obsidian note, both ways
wf ui [<ref>]                                                # print the web UI deep link for a task
wf review <ref> | --pr <url> [--repo P] | <ref> --stop       # resolve the task's diff and open it in difit, or stop the viewer
wf review comment <ref> [--format difit]                     # read a pasted review prompt from stdin
wf gc [--delete]                                             # report ledger bindings whose referent is gone; --delete drops dead records
```

A `<ref>` is anything `kata show` accepts: the issue's ULID or its short id.
There is no listing command of wf's own beyond `ready` and `escalations`;
run `kata list` directly for a full listing.

Add `--json` to `ready`, `show`, `escalations`, `workflows`, `run`, `close`
and `review` for machine-readable output. That is the protocol both clients
speak — the pi extension and the Obsidian plugin talk to wf, never to kata
directly, so the queue backend can change without touching either.

`wf show --json` emits one object. The tracker row's own fields — id,
title, priority, labels, state, lease, and so on — sit at the top level, the
same shape `ready` and `run --json` emit. Alongside them, `bindings` is the
task's own — everything no run produced — and `runs` is the ledger's history
for the task, each entry carrying what that run produced. `session` is the
one field only `show` fills in: a session is machine-local and lives in the
local ledger, never on the tracker, so `ready`, `escalations` and
`run --json` — which never load the ledger — leave it empty.

`wf ready --json`:

```json
{ "tasks": [ { "id": "01M1S…", "shortId": "neck", "title": "Add the parser",
              "priority": 1, "workflow": "plan-to-pr",
              "lease": { "actor": "wf-laptop", "stale": false },
              "note": "Research/plan.md", "needsHuman": false } ] }
```

`wf show neck --json` — one object, no `tasks` wrapper, `session` filled in,
and `runs`, which no other command emits:

```json
{ "id": "01M1S…", "shortId": "neck", "title": "Add the parser", "state": "review",
  "session": "~/.wf/sessions/neck-20260907T101956-3a1f.jsonl",
  "runs": [ { "id": "r20260907T101956-38b3", "workflow": "repro", "outcome": "done",
              "bindings": [ { "kind": "workspace", "ref": "~/.wf/worktrees/neck-add-the-parser", "state": "disposed", "meta": { "branch": "wf/neck-add-the-parser" } } ] },
            { "id": "r20260907T113012-9c0e", "workflow": "plan-to-pr", "outcome": "done" } ] }
```

`wf run neck --workflow plan-to-pr --json` emits `{ "results": [ { "task": …,
"completed": true } ] }` — or `"escalated": true` with a `reason`. A run
never closes the task; `wf close neck --json` does, emitting the evidence it
closed with.

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
  "defaultModel": "claude-sonnet-5",
  "leaseTTLSeconds": 900
}
```

`KATA_BIN`, `PI_BIN` and `DIFIT_BIN` override binaries that are
off `PATH` —
they usually are under a launchd or systemd unit. `difitCommand` (default
`npx difit`) is a shell-style command line rather than a bare binary, since
the default itself is two words; `DIFIT_BIN` replaces the whole thing.

## Collecting the dead

`wf gc` answers the one question only the ledger can answer: which recorded
workspace and session bindings point at nothing on this host — a checkout
removed by hand, a session file that would fail to reattach. `--delete` marks
those bindings missing and drops any record whose local bindings are all
missing or disposed; a bare run only reports and writes nothing. It never deletes the artifact a
record points at — a leftover checkout may hold uncommitted work. Worktrees
and branches are git's own bookkeeping: `git worktree prune` and `git branch
--list 'wf/*'` answer those questions directly.

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
the skills the profile loads. `wf run <ref> --workflow W` names the recipe
outright; without `--workflow`, selection is `wf.workflow` metadata first,
then label match. A task naming a workflow that is not loaded, or one
nothing routes, is a plain error — a human is at the terminal for every
run, and running the wrong recipe quietly under some default would be worse
than not running.

The workflows in [`workflows/`](workflows/) are the steps a task tends to
go through, and the same task runs several of them over its life:
`repro`, `file-issue`, `plan-to-pr`, `address-comments`, `pr-review`,
`diagnose-build`, `research`. Each ends in a `PR:`, a `DOC:` or an `ISSUE:`
— that is what a run has to show — and a later run on the same task picks
up the branch an earlier one pushed from.

Model lives on the workflow rather than the profile because the two vary
independently: a plan-and-implement step and a one-line triage step often
warrant different models under the very same profile. A workflow that names
none runs under `config.defaultModel`; naming neither runs under pi's own
default.

## How the pieces bind

A fact lives in exactly one place, and which place follows from what kind of
fact it is.

**Shareable facts** — the repo, PRs, filed issues, produced documents,
`wf.state`, the lease, `work.attention` — live on the tracker as plain
metadata keys. A run writes them, lists accumulating across every run on
the task, and `wf close` reads them back from there and nowhere else.

**Machine-local facts** — a run's worktree checkout, its session file — live
in the local ledger, one JSON record per task at `~/.wf/tasks/<ulid>.json`,
along with the record of each run: which workflow, which model, when, and
how it ended. They are written at spawn, before the agent produces
anything, so a crashed or hung run is still attachable through `wf attach`.
A binding like this is a fact about one machine, and is never published
anywhere another would read it back from.

**Task ↔ note.** The join is the id pair: the note's frontmatter carries
`kata-issue: <ULID>`, the half that survives a rename in Obsidian, and
`wf.doc: <path>` on the tracker is the other, written by `wf bind` or by the
first run that produces a document when the task has no note yet. Later
documents are recorded under `wf.docs` without displacing the note. Nothing
else is mirrored — titles and status live in the
tracker, prose lives in the note — and the Obsidian plugin renders the
task's managed block, its runs and what each produced, straight from `wf
show --json`.

**Agent ↔ tracker.** The seed prompt names the agent's issue, so it can read
context and comment progress itself. But claim and lease transitions belong
to `wf` alone, and the close belongs to a human — two writers on a terminal
transition produces double-closes and dropped evidence.

## What happens to a run

| Outcome | Result |
| :-- | :-- |
| Reported `DONE` **with something to show** | Run complete: output recorded on the task, worktree disposed, task left open in `review` for your next move |
| `PR:` lines | Recorded on the task; the pull request this run opened or worked on |
| `NEXT:` lines | Follow-on tasks created, linked to the parent, not launched |
| `ISSUE:` lines | Recorded on the task; enough to complete a run, never evidence for a close |
| `DOC:` lines | Moved into the vault and bound, if the workflow says so |
| `DONE` with **nothing** to show | Escalated — see below |
| No `DONE` | Escalated: flagged `needs-human`, worktree **kept**, transcript excerpted onto the task |
| Agent crashed | Same escalation path, with the failure output |

Then you decide: `wf run <ref> --workflow <next>` for the next step, or
`wf close <ref>` when the work is done. A flagged task runs again the same
way; the flag clears when the run starts.

**Silence is never success.** A run that stopped talking is not complete.

**A run with nothing to show for it is not complete.** An agent that reports
`DONE` having produced no PR, document, or issue either did nothing or forgot
to say what it did, and a human should look. The bar for a run is lower than
the bar for a close: filing an issue is a whole run's job when the workflow
was "file the issue", but it says work moved elsewhere, not that this task's
work exists, so `wf close` counts only pull requests and documents.

kata enforces the same rule independently, refusing an evidence-free close.
`wf close` refuses too, rather than synthesizing evidence: invented evidence
is precisely what would make a closed task worthless.

## Worktrees across runs

A task's first run with a checkout cuts a branch, `wf/<short-id>-<title>`.
The branch outlives the checkout: a completed run disposes its worktree and
the next run on the task — the fix after the repro, the comment round after
the PR — gets a fresh checkout of that same branch, so it lands where the
earlier run pushed from. An escalated run keeps its checkout, and the next
run continues in it, half-done state and all. `git branch --list 'wf/*'`
lists what is left once a task is closed.

## Review

`wf review <ref>` resolves a task to something a human can look at and opens
it in [difit](https://github.com/yoshiko-pg/difit), a local diff viewer. It
tries, in order, the first rung that matches:

1. **A PR.** If the task recorded one (`wf.pr` metadata, written when a run
   reports `PR:`), difit opens `--pr <url>` — the shipped truth once one
   exists.
2. **A live worktree.** If the run's checkout (its workspace binding in the
   local ledger) is still on disk, difit opens it directly with
   `--include-untracked` — this is the rung that matters most, because an
   escalated run that produced no PR is exactly what a human needs to look
   at, and its checkout is the only place the agent's untracked and
   uncommitted state still lives.
3. **A surviving branch.** If the worktree was disposed, difit diffs the
   branch its binding recorded against the repo's base branch with
   `--merge-base`.
4. **A bound note.** A workflow that only produced a document has no diff at
   all; `wf review` reports the vault path and opens nothing.

`wf review --pr <url> [--repo <path>]` reviews any PR directly, with no kata
running at all. Use it for a PR a human opened by hand, or one that predates
`wf.pr` metadata ever being recorded. `--pr` needs no task and no kata: it
builds the review target from the URL alone, and its remembered port is
keyed by the URL's handle (`owner/repo#123`) so difit's comments come back
on reopen. `--repo` defaults to `config.Repo`. It participates in the same
one-viewer-at-a-time lifecycle as a task review, and the positional `<ref>`
and `--pr` are mutually exclusive — passing both is a usage error. A URL
with no task behind it has no filed issues, so nothing is seeded.

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
- **`internal/supervisor`** runs whole dispatches against real kata, real
  git worktrees, and a stub agent — completing a run and closing the real
  issue, escalating and keeping the worktree, continuing a re-run in that
  kept checkout, moving a produced document into a vault and binding it
  both ways, and spawning a linked follow-on.
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
reorganization. The plugin also owns the note's managed task block: it
renders a bound note's runs and bindings straight from `wf show --json` on
open, so wf itself never writes prose into your vault.

## Layout

```
cmd/wf/                command dispatch
internal/wf/           task model, outcome protocol, leases, sessions, apply
internal/supervisor/   one dispatch: lease, workspace, agent, outcomes, release
internal/store/        the local ledger: runs and machine-local bindings
internal/gc/           sweeps the ledger for dead local bindings
internal/workflow/     canned workflows
internal/kata/         kata queue backend (the only wire-format assumptions)
internal/runner/       pi runner
internal/workspace/    git worktrees
internal/note/         frontmatter read/write only — the plugin renders the note
internal/review/       target ladder, difit lifecycle, finding seeds
internal/config/       settings
workflows/             example workflows to copy into ~/.wf/workflows
```

Leases and the work-state vocabulary live in `internal/wf`, deliberately
above the backend interface: no tracker in scope implements either, so
pushing them down would mean writing them once per adapter.

## Design

[DESIGN.md](DESIGN.md) says what wf is and where the seams sit — kata,
pi, and git worktrees as the three adapters, and the non-goals that keep
it a wrapper rather than a platform. [DESIGN-task.md](DESIGN-task.md) is
superseded: it is where the local ledger came from, and it still holds the
multi-host and re-run findings that justified keeping one. [DESIGN-slim.md](DESIGN-slim.md)
is the plan that cut wf back down to size — what got deleted at each stage
and why, and what the numbers looked like before and after.
