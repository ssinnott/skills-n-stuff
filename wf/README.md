# wf

A workflow CLI that runs agent work off a queue.

`wf` reads ready work from a tracker, leases it, gives it an isolated
workspace, runs a coding agent in it, parses the agent's outcome verbs, and
writes results back as comments, links, and evidence-backed closes. The
interactive agent session is a client of this CLI, not its host — restarting
your TUI means nothing to running work.

kata is the first queue backend, pi the first runner, git worktrees the first
workspace. See [DESIGN.md](DESIGN.md) for why the seams sit where they do,
and what was rejected on the way.

## Status

Early. The read side and the bindings work; the dispatch loop does not.

| Command | State |
| :-- | :-- |
| `wf ready` / `wf show` / `wf escalations` | implemented |
| `wf attach` / `wf bind` / `wf ui` | implemented |
| `wf run` | refuses — needs the worktree and pi runner seams |

The kata adapter is written against kata's published command reference, not
against a running daemon. Flags are quoted from the docs; the *shape* of the
JSON kata returns is undocumented and inferred. All of that guesswork lives in
`NormalizeIssue` and `ExtractIssues` in `internal/kata/kata.go` — treat the
first live run as protocol discovery, and fix it there.

## Build

```sh
go build -o wf ./cmd/wf
go test ./...
```

No dependencies beyond the standard library.

## Usage

```sh
wf ready --limit 10        # actionable work, top of queue first
wf show abc4               # one task, with its lease and session
wf escalations             # tasks flagged needs-human
wf attach abc4             # reopen the pi session that ran this task
wf bind abc4 notes/plan.md # bind a task to an Obsidian note, both ways
wf ui abc4                 # deep link into kata's web UI
```

Set `KATA_BIN` if `kata` is not on `PATH` — it usually isn't under a launchd
or systemd unit.

## How the pieces bind

**Task ↔ session.** Every run records `pi.session` (the session file path),
`pi.session_id`, `pi.workspace`, and appends to a `pi.session_history` JSON
array. These are written *at spawn*, before the agent produces anything, so a
crashed or hung run is still attachable — those are the runs you most need to
read. `wf attach` resolves them and execs `pi --session <path>`.

**Task ↔ note.** The note's frontmatter carries `kata-issue: <ULID>`; the task
carries `obsidian.note: <path>`. Nothing is mirrored: titles and status live in
the tracker, prose lives in the note, and the only shared state is the id pair.
The ULID is stored rather than the short id because kata documents it as the
ref that survives renames and moves.

**Agent ↔ tracker.** The worker's seed prompt names its issue, so an agent can
read context and comment progress itself. But claim, close and lease
transitions belong to `wf` alone — two writers on a terminal transition
produces double-closes and dropped evidence.

## Layout

```
cmd/wf/            command dispatch
internal/wf/       task model, outcome protocol, leases, session binding
internal/kata/     kata queue backend (the only wire-format assumptions)
internal/note/     Obsidian frontmatter binding
```

Leases and the work-state vocabulary live in `internal/wf`, deliberately above
the backend interface: no tracker in scope implements either, so pushing them
down would mean writing them once per adapter.
