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

[DESIGN-slim.md](DESIGN-slim.md) proposes cutting wf back to this description,
and names which of the pieces built since then stay.

[DESIGN-task.md](DESIGN-task.md) is **built**, and supersedes this document
wherever the two disagree about what a task is. The task here is a normalized
queue row with bindings stuck to it as ad-hoc metadata keys; there it is wf's
own object — an identity, a history of runs, and typed, plural, stateful
bindings — held in a local ledger beside this config. The seams, the outcome
protocol and the no-workflow-engine rule below all still stand, and the
metadata key names in this document are the pre-rename ones (`pi.session` and
friends), which are read for one release and never written.

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

- **The agent session is bound to the task by metadata, written at spawn.**
  (Superseded by DESIGN-slim.md stage 5: the session is recorded in the
  local ledger, never in tracker metadata.)
  `wf.session` (file path), `wf.session_id`, `wf.workspace`, and a
  `wf.session_history` JSON array — role names, with the runner carried as a
  value on the binding rather than as half of a key. The `pi.*` names these
  replaced are read for one release and never written. Written *before* the
  agent produces anything, so a crashed or hung run is still attachable — `wf attach
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
  later; worktree is the only implementation that ships. A worktree is
  disposed on a clean close and *kept* on an escalation, because the
  checkout is the evidence a human needs to diagnose the run.

- **A canned workflow is dispatch wiring, not agent judgment.** A workflow
  file names a profile, a model, a prompt, resources, and where artifacts
  land — it does not contain the expertise. That stays in the skills the
  profile loads, exactly as pi-tasks decided when it refused to ship
  workflow skills. Selection is by task metadata (`wf.workflow`) first, then
  by label, so a queue can route work by labelling it and a one-off task can
  still override. A task naming a workflow that is not loaded escalates
  rather than falling back to a default: running the wrong recipe quietly
  is worse than not running. Rejected: workflows as executable definitions
  (that is the workflow engine this design exists to avoid) and selection
  by title parsing (invisible and unqueryable).

- **Model is a workflow property, not a profile property.** A profile
  decides what an agent can reach — tools, skills, permissions. A model
  decides how much thought is worth spending, and that varies by step, not
  by role: the same coding profile plans under one model and does a
  mechanical rename under a cheaper one. A workflow with no `model` runs
  under `config.defaultModel`; wf never picks a model on its own beyond
  that config default, since guessing "how hard is this task" from a title
  is exactly the kind of silent judgment call this design keeps out of the
  dispatcher.

- **Workflow files are markdown with flat frontmatter.** Scalars and
  comma-separated lists, matching the skill and command files these sit
  alongside. Flat because Go's standard library has no YAML parser and a
  dependency is not worth five keys. Rejected: nested YAML via a
  third-party parser, and JSON (unreadable for a file that is mostly a
  prompt).

- **Artifacts bind into the vault on the way out.** A workflow that
  declares `bind-docs` has its `DOC:` outcomes moved into the vault and
  linked to the task in both directions. Evidence and comments then cite
  the *final* location, so a closed task never points into a worktree that
  has been disposed. Collisions get a numeric suffix rather than an
  overwrite: two runs producing `plan.md` are two documents.

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

- **A completion with no evidence is not a completion.** An agent that
  reports DONE having produced no PR, commit, document or test either did
  nothing or failed to say what it did; either way the ledger should not
  record the task as finished until a human looks. kata independently
  enforces the same rule — it refuses `close --done` without typed evidence
  and tells you to leave the issue open — which is corroboration rather than
  the reason. Rejected: synthesizing evidence to satisfy the check. Evidence
  that wf invented is exactly what makes a closed task worthless, and the
  close discipline is the main reason to be on this tracker at all.
  A filed `ISSUE:` deliberately does not count: it says work moved
  elsewhere, not that this task's work exists. Triage workflows ask their
  agent for a writeup, which does.

- **wf.repo, wf.pr and wf.issue are wf's own memory, not a read of kata's
  evidence.** `Close` turns `CloseResult` into repeated
  `--evidence pr:<url>` flags, but `NormalizeIssue` never reads an evidence
  field back — kata's wire format there is undocumented, and guessing at it
  would repeat exactly the mistake the five wire-format surprises above were
  fixed to stop making. So a run's repo, PRs and filed issues are recorded
  as wf's own metadata at the same seam that already applies outcomes,
  independent of how the run settles: an escalated run that still opened a
  PR, or named its checkout, leaves that trace right there rather than
  nowhere. This is what makes `wf review`'s first rung — jump straight to a
  task's PR — possible at all without shelling out to `gh` or parsing a
  format nothing pins.

- **`wf review`'s target ladder tries the strongest evidence first, and
  falls through in the order a human would.** A shipped PR is the truth
  once one exists. A live worktree beats a pushed branch even though a
  branch is "more finished," because the worktree still holds whatever the
  agent left untracked or uncommitted — exactly the state an escalated run
  (which is kept on disk for precisely this reason) needs a human to see.
  A branch is what is left once that checkout is disposed. A bound note is
  what is left when there was never a diff at all. Every rung reads a fact
  that already exists on the task or in config; the ladder invents nothing,
  including a repo path when PR evidence is not there — that would be
  fabricating a place to look, not reporting one. Case 2's disk check
  (`os.Stat`) is what keeps a disposed worktree from being handed to difit
  as if it were live.

- **One review pane, replaced rather than accumulated.** `wf review`
  records the live viewer (ref, pid, port, url) next to wf's own config, the
  same neighborhood as sessions and worktrees, and a new invocation kills
  the previous pid before spawning. This mirrors the one-worktree-per-task
  rule: a second background difit server nobody is looking at is the same
  kind of mess as a second agent in the same checkout, just quieter about
  it. Rejected: one viewer per task, left running — orphaned Node
  processes accumulate silently, and there is only one human looking at
  one diff at a time regardless of how many tasks are in flight.

- **`wf review --pr <url>` is a second, ad-hoc entry point, not a fifth
  ladder rung.** Rung 1 only fires when `wf.pr` metadata exists, which
  `recordRunFacts` writes only when an agent reported `PR:` — a PR a human
  opened by hand, or one that predates any of this, has no task and can
  never reach the ladder no matter how the ladder itself is reordered. So
  `--pr` skips task lookup and the queue entirely (it works with no kata
  running), builds a `Target` directly, and shares only the lifecycle a
  task review already has: kill-the-previous-viewer, remember-the-port,
  record-in-review.json. Its `Result.Ref` is a short handle derived from
  the URL (`owner/repo#123` for GitHub, the raw URL otherwise) since there
  is no task ShortID to report and the JSON contract requires a non-empty
  `ref`.

- **Port memory: a remembered port beats a computed one.** difit's
  comments live in the browser's own `localStorage`, scoped per *origin* —
  `localhost:<port>` — and difit silently falls back to a different port
  when its preferred one is occupied (verified: two instances asking for
  4980 got 4980 and 4981 back; it also steps over ports held by unrelated,
  non-difit processes). A task reopened on a different port therefore shows
  an empty comment store even though the earlier comments are still
  sitting on the origin nothing returns to. `review.json` now carries a
  ref→port map alongside the single live-viewer record; on open, a ref
  with a remembered port asks difit for that port again via `--port`, and
  whatever difit *actually* reports back — never the port that was asked
  for — is what gets remembered. The map is capped at the 50 most recently
  used refs, oldest evicted first, so the file cannot grow without bound
  on a long-lived install. Additive: a `review.json` from before this
  existed has no `ports` key and loads as an empty map, not an error — the
  same tolerance `LoadState` already gives a wholly missing file.
  Rejected: **a deterministic port hashed from the ref** (so the same task
  always asks for the same port without persisting anything). It sounds
  like it should be strictly better, but it isn't: a hash can collide with
  another task's hashed port, or with an unrelated process, exactly as
  easily as the port-in-use case this whole feature exists to work around
  — and it throws away the one fact that actually is reliable, which is
  what difit itself reported binding last time. An observed port beats a
  guessed one even when the guess is reproducible. The honest limit
  either way: reuse is *likely*, never *guaranteed* — a foreign process
  squatting the remembered port still costs that task its previous
  comments, and nothing wf does can prevent that.

- **Findings seed the viewer; comments come back by a clipboard, or a
  harvest, not a pipe.** Machine → human is automated: a task's `ISSUE:`
  outcomes that name a file and line become difit's `--comment` threads at
  spawn time, because that information already exists and rendering it
  costs nothing. Human → machine cannot be automated the same way: difit
  keeps comments in the browser's own `localStorage` and ships no endpoint
  to read them back (verified against v5.0.12 — `/api/diff` exists,
  `/api/comments` 404s), so `wf review comment <ref>` reads whatever a
  human pasted from difit's "Copy All Prompt" button off stdin and appends
  it to the task, prefixed so it reads as review feedback rather than an
  agent's own comment. `--format difit` is the same idea without the
  human hop: the Obsidian plugin harvests difit's actual `localStorage`
  entries out of the browser frame and pipes the JSON in directly.
  **The storage key shape here is verified by observation (a live difit
  v5.0.12 page under Playwright), not documented anywhere** — there is no
  bare `difit-storage-v1` key at all; the real keys are namespaced per
  repo-hash and commit-range (`difit-storage-v1/<hash>/<base>-<target>`,
  `__default__` in place of a hash), one origin can hold several, and each
  key's value is a JSON *string* holding a `{baseCommitish, targetCommitish,
  threads: [...]}` object. `ParseDifitStore` accepts that shape plus three
  more tolerant ones (pre-parsed values, a single bare store object, a bare
  thread array) and merges every key rather than picking one, since a
  harvest can span several reviewed commit ranges at once. A thread wf
  itself seeded via `--comment` lands back in this same store as an
  ordinary thread once difit has rendered it — harvesting after a review
  round-trips wf's own findings back onto the task as if a human had
  re-typed them. This is deliberately not deduplicated: the task comment a
  harvest produces is a transcript of the review session, not a
  deduplicated set of findings, and a finding a human explicitly kept
  open by leaving a message under it is exactly the kind of signal a
  transcript should preserve rather than silently drop. The asymmetry
  between the two directions is real, not an oversight: one direction is
  wired through data wf already has, the other is a hop through a UI
  action (or, now, a harvest of browser storage) a script cannot trigger
  through an API. Rejected: **polling difit for comments** — there is no
  endpoint to poll, verified rather than assumed, so this was never a
  timing problem to solve. Rejected: **a native diff renderer** built into
  wf — difit already does threaded, line-anchored review with a UI worth
  pasting text
  out of, and duplicating that (in Go, in a terminal) to save one clipboard
  step is a worse trade than the asymmetry it would remove.

- **Interactive pi is a client, not the host.** The pi extension registers
  slash commands that shell out to `wf`; it holds no orchestration state.
  Restarting the TUI means nothing to running work. Rejected: an extension
  hosting the fleet in-process via the SDK — it couples worker liveness to
  the one process you restart most, and background workers have no
  `ctx.ui` to approve through anyway.

- **Obsidian is the prose layer, joined by an id pair.** Note frontmatter
  carries `kata-issue: <ULID>`; the issue carries `wf.doc: <path>` (which
  `obsidian.note` was renamed to, for the same reason: the key names the
  role, and the store is a value on the binding).
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
- [x] Worktree workspace: one per task on its own branch, disposed on a
      clean close and kept on escalation. Tested against real git.
- [x] pi runner: wf mints the session path and passes `--session`, so the
      binding is writable before the agent produces anything; the run's
      output is captured for outcome parsing.
- [x] Canned workflows: markdown + flat frontmatter, selected by metadata
      or label, with profile, resources and artifact binding.
- [x] Applying outcomes: close with evidence, materialize NEXT as a linked
      sibling, record filed issues without gating, escalate on anything
      that did not report DONE.
- [x] Artifact binding: DOC outcomes move into the vault and link both ways.
- [x] `wf run --once` and `wf run --max N`: leases, renewal while running,
      stale reclaim, concurrency cap.
- [x] Integration tests against a real kata daemon (v0.16.0): create, get,
      ready, metadata, leases, claim conflicts, release, close with evidence,
      escalation queries, idempotent follow-ons.
- [x] End-to-end tests: real kata plus real git worktrees plus a stub agent,
      covering close, escalate-and-keep, artifact binding into a vault,
      linked follow-ons, and concurrent runs.
- [ ] Live validation against a real pi install (needs a provider key, so
      the suite uses a stub agent instead).
- [x] JSON protocol (`--json` on the read commands and on `run`), which is
      what both clients speak instead of reaching into kata.
- [x] pi extension exposing `/wf`, including `attach` switching the live
      session into a worker's session.
- [x] Obsidian: agent queue pane, framed kata UI following the active note,
      bind command, and rename sync.
- [ ] Retry with backoff and a dead-letter state, if escalation-only proves
      too blunt in practice.
- [x] `wf review`: the target ladder (PR, live worktree, surviving branch,
      bound note), difit spawn/parse/kill behind a stubbed seam, one
      recorded viewer at a time, findings seeded as `--comment` threads,
      and `wf review comment` for the human side of feedback.
- [x] `wf review --pr <url>`: an ad-hoc PR review bypassing the ladder and
      the queue entirely; per-ref port memory in `review.json` so a
      reopened task is likely to land on the same browser origin as its
      earlier comments; `wf review comment --format difit` to harvest
      difit's own comment store instead of a pasted prompt.

## What the live protocol turned out to be

The adapter was first written from kata's published reference. Running it
against kata v0.16.0 contradicted that reference in five places, every one
now pinned by an integration test:

- `claim` has no `--if-unowned`. An unqualified claim already refuses an
  owned issue with `already_claimed`, which is the semantics wf wanted.
- `close` takes no `--idempotency-key` or `--if-match`, and its `--pr` and
  `--commit` sugar take a single value, so multiple PRs go through repeated
  `--evidence pr:<url>`.
- `close --done` demands a message of at least 40 characters *and* at least
  one piece of typed evidence. Both shaped behavior above.
- Releasing ownership is `edit --owner ""`. `assign` rejects an empty owner,
  and there is no unclaim verb at all.
- Issues carry both an integer `id` and a 26-character `uid` ULID, and
  `show` returns labels as objects beside the issue while `list` and `ready`
  return them as strings inline. Reading the first present key would have
  bound every note and session to the integer.

The lesson generalizes: `NormalizeIssue` stays the single point of contact
with kata's wire format, and the integration tests are what keep it honest.

## Risks

- **Output-tail parsing.** Outcome verbs are recovered from the agent's
  final message. A runner that truncates or reformats that message breaks
  the waist. Mitigation: the parser scans the whole transcript, not just the
  last line, and an unparseable run escalates rather than closing.
- **Unverified pi argv.** `BuildArgs` assumes print mode takes the prompt
  positionally and `--session` accepts a path that does not yet exist. The
  integration tests use a stub agent, so this is the one assumption the
  suite does not cover — pi needs a provider key to run at all.
- **Lease TTL versus long tasks.** A TTL short enough to reclaim dead
  workers promptly is short enough to steal a slow one. Mitigation:
  renewal on liveness rather than a fixed deadline.
- **Two writers on the issue.** wf and the agent both write comments. Only
  wf writes lifecycle. If that rule slips, closes race.
- **Retry policy is absent by design.** A run is attempted once per `wf run`
  invocation; escalation is the only recovery. Anything cleverer needs a
  backoff and a dead-letter state, which is the point at which the event
  orchestrator this design rejected starts earning its keep again.
- **difit's own CLI surface is unverified against any published reference,**
  the same category of risk as pi's `BuildArgs` above: `--pr`,
  `--merge-base`, `--include-untracked`, `--comment`'s JSON shape, and the
  background-mode JSON line were confirmed empirically against v5.0.12, not
  against documentation, and its exit code is already known not to be a
  reliable success signal. Mitigation is the same shape as pi's: the seam
  is narrow (`Spawner.Spawn` plus `ParseSpawned`), so the first live run
  that disagrees names exactly what to change.

## Done means

`wf run --once` picks the top ready issue off a real kata daemon, leases
it, builds a worktree, runs pi in it, and closes the issue with evidence
the agent actually produced — and `wf attach <ref>` drops you into that
exact session afterwards.
