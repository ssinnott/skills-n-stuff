---
status: in-progress
pi-session: —
---

# wf: a workflow CLI over pluggable queues

## Goal

One CLI that runs agent work off a queue. It reads ready work from a
tracker, leases it, gives it an isolated workspace, runs a coding agent in
it, parses the agent's outcome verbs, and writes the results back to the
tracker as comments, links, and evidence-backed closes. The interactive
agent session becomes a client of the same CLI, not its host.

kata is the first queue backend, pi the first runner, git worktrees the
first workspace. The seams exist so the second of each is an adapter
rather than a rewrite.

## Non-goals

- No workflow engine. The graph is `ready → lease → run → apply → release`,
  one step deep. Durable multi-step execution state is a problem this
  doesn't have; the ledger and the agent's own session file already hold
  everything worth resuming.
- No daemon of our own. kata already runs one, owns the durable event
  cursor, and ships two UIs over it. `wf run` is a loop, not a service.
- No second source of truth. wf stores no issue state; every fact it acts
  on is read from the queue backend and every result is written back to it.
- No tracker features. Priorities, dependencies, scheduling and search are
  the backend's job. wf reads `ready` and trusts it.
- Not a chat client. Steering a running worker means attaching to its
  session with pi directly; wf tells you which session that is.

## Decisions

- **Three seams, one implementation each.** Queue (kata), runner (pi),
  workspace (git worktree). The interfaces exist from the start so the
  shape is honest, but only one adapter per seam ships until a second is
  actually needed. Rejected: writing GitHub/Linear adapters up front —
  an interface derived from one real implementation and one imagined one
  is an interface derived from the imagined one.

- **The outcome protocol is the narrow waist.** Agents end with
  `DONE / PR / ISSUE / NEXT / DOC / REPO` lines (inherited unchanged from
  pi-tasks). Because those verbs define what "apply the result" means
  independent of any tracker, a queue backend needs seven methods rather
  than a translation layer. kata's `close --done --message --pr --commit
  --reviewed --evidence` maps onto them almost exactly, which is the
  clearest evidence the waist is in the right place.

- **Leases live above the backend interface.** No tracker in scope has
  lease semantics: kata documents `claim` but no expiry and no `release`
  verb at all, GitHub Projects has none, Linear has none. Implementing
  leasing per adapter means writing it three times, differently, and
  debugging stale claims forever. The core owns actor/host/pid/timestamp/
  TTL and stale reclaim; adapters supply only *storage* for that record
  (kata metadata, elsewhere a field or label). Rejected: `claim` as the
  lease primitive — it records ownership, not liveness, so a killed worker
  holds its issue forever.

- **State vocabulary lives above the interface too.** kata's status is
  binary open/closed; Linear has real workflow states; GitHub Projects has
  custom single-select fields. wf defines `ready / claimed / running /
  review / blocked / needs-human / done` and each adapter maps it. On kata
  the mapping is metadata, following the convention kata's own docs use
  (`work.attention=needs-human`), so the escalation queue is a plain
  `kata list --meta` and renders for free in the CLI, TUI and web UI.

- **The pi session is bound to the task by metadata, written at spawn.**
  `pi.session` (file path), `pi.session_id`, `pi.workspace`, and a
  `pi.session_history` JSON array. Written *before* the agent produces
  anything, so a crashed or hung run is still attachable — `wf attach
  <ref>` resolves the metadata and execs `pi --session <path>`. Rejected:
  recording the session on completion (loses exactly the runs you most
  need to inspect) and deriving session names from the issue ref (pi
  organizes sessions by working directory, so a bare id is ambiguous
  across worktrees).

- **The agent is told its issue ref; wf owns the lifecycle.** The worker's
  seed prompt carries the ref, and the worker profile can load kata's CLI
  or MCP server, so an agent can read context and comment progress itself.
  But claim, close and lease transitions are wf's alone. Rejected: letting
  agents close their own issues — two writers on the terminal transition
  produces double-closes and silently dropped evidence.

- **One worktree per task.** Two agents in one checkout is the failure that
  costs an afternoon. The workspace seam exists so docker/ssh can arrive
  later; worktree is the only implementation that ships.

- **Go, standard library only.** A single static binary runs as a launchd
  or systemd service with no runtime to install, which is what a supervisor
  loop wants. The decisive argument is kata: it is written in Go and
  supports embedding its listener-free HTTP service in-process, so the
  backend can eventually hold the ledger directly instead of shelling out
  to a CLI and parsing undocumented JSON — which is this design's largest
  standing risk. Rejected: TypeScript sharing a core package with the
  Obsidian plugin. Sharing a package is a weaker benefit than it looks —
  the plugin can call `wf --json` across a subprocess boundary, which is
  the same contract it would have with kata itself, and a cleaner one than
  a shared build.

- **The kata adapter shells out first, embeds later.** v1 drives the `kata`
  CLI with `--json`: it works today, adds no dependency, and inherits daemon
  discovery, auth and project resolution for free. In-process embedding is a
  second implementation of the same interface, taken once the CLI path has
  proved the semantics. Rejected: embedding immediately — it couples the
  first working version to a Go API that has to be learned and pinned before
  anything runs end to end.

- **Interactive pi is a client, not the host.** The pi extension registers
  slash commands that shell out to `wf`; it holds no orchestration state.
  Restarting the TUI means nothing to running work. Rejected: an extension
  hosting the fleet in-process via the SDK — it couples worker liveness to
  the one process you restart most, and background workers have no
  `ctx.ui` to approve through anyway.

- **Obsidian is the prose layer, joined by an id pair.** Note frontmatter
  carries `kata-issue: <ULID>`; the issue carries `obsidian.note: <path>`.
  ULID rather than short id because kata documents it as the ref that
  survives renames and moves. Nothing is mirrored — titles and status live
  in the tracker, prose in the note, and the only shared state is the pair.
  Rejected: replicating issues into notes (a second store, and the "board
  is a projection" rule from pi-tasks exists precisely to prevent it).

## Rejected approaches

- **A general event orchestrator** (Temporal, Inngest, Restate, DBOS).
  Their product is durable multi-step workflow state; this workflow is one
  step whose progress is already durable in two places. It would buy a
  retry loop and a dashboard at the cost of a server, and would solve none
  of the actually hard parts — worktree isolation, approval policy for an
  agent that cannot prompt, escalation routing, evidence quality.
  Revisit when two of these are true: multi-day human waits needing durable
  timers, fan-out/fan-in across many repos, more than one machine, or
  executions outliving a deploy.

- **Adopting kata-symphony.** A same-named but unrelated project
  (gannonh/kata-symphony, kata.sh) that does orchestrate issues to PRs with
  pi as its runner. It polls GitHub Projects v2 and Linear, not katatracker,
  so it cannot read this queue. Kept as a reference architecture — it
  validates the supervisor-daemon-plus-thin-client shape — and as a possible
  upstream if its tracker abstraction ever takes a kata backend.

- **An in-process fleet inside the interactive pi.** See the client/host
  decision above.

## Build plan

- [x] DESIGN.md: seams, waist, and what lives above the interface.
- [x] Core types: `Task`, `Outcome`, `Lease`, `WorkState`, and the
      `QueueBackend` / `Runner` / `Workspace` interfaces.
- [x] Outcome parser: the six verbs, tolerant of surrounding prose,
      with tests over realistic agent transcripts.
- [x] Lease encode/decode/staleness, backend-independent, with tests.
- [x] Session binding: metadata keys, spawn-time write, attach resolution.
- [x] kata adapter: `ready`, `get`, `claim`, `release`, `comment`,
      `close`, `create`, over the CLI's `--json` output, with the wire-format
      guesswork isolated in one function and pinned by tests.
- [x] Obsidian binding: frontmatter read/write, and `wf bind` writing both
      sides of the id pair.
- [x] Read-side commands: `ready`, `show`, `escalations`, `attach`, `ui`.
- [ ] Worktree workspace: create per task, prune on release.
- [ ] pi runner: spawn `pi --mode json`, stream events, capture the tail
      for outcome parsing, honor an abort signal.
- [ ] `wf run --once`: one task end to end, no concurrency.
- [ ] `wf run --max N`: the supervisor loop, lease renewal, stale reclaim.
- [ ] Live validation against a real kata daemon and a real pi install
      (neither exists in the build environment).

## Risks

- **Unverified CLI surface.** The kata adapter is written from published
  docs, not against a running daemon. `release` in particular has no
  documented verb — the adapter clears the lease record and best-effort
  clears the owner, and that path is guessed. First live run should be
  treated as protocol discovery.
- **Output-tail parsing.** Outcome verbs are recovered from the agent's
  final message. A runner that truncates or reformats that message breaks
  the waist. Mitigation: the parser scans the whole transcript tail, not
  just the last line, and an unparseable run escalates rather than closing.
- **Lease TTL versus long tasks.** A TTL short enough to reclaim dead
  workers promptly is short enough to steal a slow one. Mitigation:
  renewal on liveness rather than a fixed deadline.
- **Two writers on the issue.** wf and the agent both write comments. Only
  wf writes lifecycle. If that rule slips, closes race.

## Done means

`wf run --once` picks the top ready issue off a real kata daemon, leases
it, builds a worktree, runs pi in it, and closes the issue with evidence
the agent actually produced — and `wf attach <ref>` drops you into that
exact session afterwards.
