---
status: proposed
supersedes: —
---

# The task object: bindings as a first-class concept

A proposal against [DESIGN.md](DESIGN.md), not a replacement for it. The
seams, the outcome protocol and the no-workflow-engine rule all stand. What
changes is that the thing they all operate on stops being a queue row with
metadata stuck to it and becomes wf's own object.

## Goal

A task is an **identity plus a set of typed bindings**: the worktrees,
workflow runs, agent sessions, pull requests, documents, filed issues and
review panes that accumulate around one piece of work, each recorded with
its own lifecycle rather than as a loose string.

Standalone means two things, both load-bearing:

- **Nothing in the vocabulary names a product.** A session's runner is `pi`
  as a *value*; a queue's backend is `kata` as a *value*; a document's store
  is `vault` as a *value*. Today those names are keys — `pi.session`,
  `obsidian.note` — which is the same mistake pi-tasks made by hanging its
  task concept off pi, one level down.
- **A task exists whether or not a tracker row does.** Starting work is not
  the same act as filing an issue, and today it has to be.

## What is actually missing today

`wf.Task` is a normalized queue row — id, title, priority, labels, a
`map[string]any` of metadata. Everything that binds work to the world lives
in that map as an ad-hoc key, and every consumer re-derives the join:

- `BindingFromMeta`, `HistoryFromMeta`, `PRsFromMeta`, `IssuesFromMeta`, and
  three separate hand-rolled `Meta[ObsidianNoteKey].(string)` reads in
  `cmd/wf/main.go`, `cmd/wf/output.go` and `internal/review/review.go`. Each
  one re-implements its own tolerance for a malformed value.
- `review.Inputs` **is the missing type**, already written out by hand at one
  call site. It is literally "everything bound to this task": repo,
  worktree dir, branch, whether that branch still exists, base, first PR,
  bound note. Assembled fresh from five metadata reads, one derivation and
  one `os.Stat` every time anyone wants to look at a task.

Five concrete failures follow from that shape, and none of them is stylistic:

- **Machine-local paths live in shared tracker state.** `pi.session` and
  `pi.workspace` are absolute paths on one host, written into a ledger two
  hosts share. Config already contemplates this — `actor: "wf-laptop"` — so
  a laptop and a desktop running against one kata silently overwrite each
  other's session and checkout, and `wf attach` hands you a path that does
  not exist on the machine you are sitting at.
- **Bindings are single-valued where the work is plural.** One
  `obsidian.note`: the first produced document wins and the rest survive
  only as prose in a comment. One `pi.workspace`: a re-run overwrites the
  checkout of the escalated run a human was on their way to inspect —
  the checkout wf deliberately kept for exactly that purpose.
- **Bindings carry no lifecycle.** Nothing records that a worktree was
  disposed; `wf review` finds out by `os.Stat`. Nothing records that a PR
  merged, so wf cannot do what pi-tasks could — complete a task when all of
  its PRs land.
- **The branch is derived, not recorded.** `workspace.WorktreeName` builds
  it from the task's *title*. Rename a task in kata and rung 3 of the review
  ladder looks for a branch that no longer answers to that name, while the
  real branch sits in the repo unreferenced. A fact that a run established
  is being recomputed from a field a human is free to edit.
- **Work with no tracker row cannot be a task at all.** That is not a
  hypothesis: `wf review --pr` exists as a documented second entry point
  that "bypasses the ladder and the queue entirely" precisely because a PR
  a human opened by hand has nowhere to hang.

## Decisions

- **A task is an identity plus typed, plural, stateful bindings.** One
  envelope — `{kind, ref, label, state, at, payload}` — over a closed set of
  kinds: `queue`, `repo`, `workspace`, `workflow`, `session`, `pr`, `doc`,
  `issue`, `review`. Every kind is a list; "the current one" is a query over
  it (the newest live binding of that kind), not a separate key. Rejected:
  a free-form key/value bag with better names — that is what exists, and
  names are not the problem; the absence of a type every reader shares is.

- **The vocabulary names roles, not products.** `session.runner = "pi"`,
  `queue.backend = "kata"`, `doc.store = "vault"`. This is the whole content
  of "standalone": a second runner or a second prose layer becomes a value
  in an existing field instead of a parallel set of keys with a parallel set
  of readers. Rejected: keeping `pi.*` and `obsidian.*` as key names for
  compatibility — the migration is a one-time read-both-write-new, and
  carrying the names forward carries the coupling forward with them.

- **The ledger is local; the tracker keeps work state. Split by
  durability, not by convenience.** A binding points at something that lives
  on one machine — a checkout, a session file, a browser origin — and is
  exactly as durable as the thing it points at. A worktree path is
  meaningless on another host, so co-locating the record with the artifact
  is not a compromise, it is correctness. Title, body, priority, labels,
  open/closed and queue order stay reads against the tracker; wf still
  stores no issue state, and DESIGN.md's "no second source of truth" holds
  for every fact it was written about.

  This is less a new store than the one that already exists growing up:
  `review.json` is *already* a local per-ref binding ledger — a live viewer
  plus a ref→port map, written beside wf's config, capped and evicted —
  built for exactly this reason and currently the only binding wf keeps
  honestly.

  Rejected: **a typed envelope stuffed into tracker metadata.** It gets the
  types and none of the rest — the multi-host clobber stays, every read is
  a subprocess round-trip through a wire format DESIGN.md already names as
  the standing risk, and a task still cannot exist before a row does.

- **Shareable bindings are published to the tracker, one way, never read
  back.** PRs, documents, filed issues and `wf.state` keep being written as
  metadata exactly as today, so kata's CLI, TUI and web UI keep showing what
  they show now. Publication is derived output from the ledger. This is
  already wf's posture on `wf.pr` — written as wf's own memory, deliberately
  never read back, because kata's evidence format is undocumented — the
  decision just names it and applies it uniformly.

- **wf mints the id; the tracker row is a binding.** `wf task new "fix the
  parser"` works with no kata running. Adopting a tracker row *adds* a
  `queue` binding rather than replacing identity, and `wf show <ref>`
  resolves a ref against wf's id, its short handle, the tracker id or the
  tracker's short id. Rejected: **reusing kata's ULID as wf's primary key**
  — it reads as free (no mapping table) and costs the two things this
  proposal is for: identity is unavailable until a row exists, and the
  backend is re-bound to wf at the one place hardest to change later.

- **The review ladder becomes a query.** `Resolve`'s four hardcoded rungs
  over hand-assembled inputs become: order the task's bindings by strength
  (`pr` > live `workspace` > `workspace` branch > `doc`) and take the first
  one that is live. `os.Stat` moves from being a rung's private check to
  being how a `workspace` binding's state is refreshed. `wf review --pr
  <url>` stops being a bypass and becomes an ordinary task carrying exactly
  one `pr` binding — the same code path, one binding shorter.

- **One JSON file per task under `~/.wf/tasks/`.** Greppable, diffable, no
  daemon, no migration tooling, inspectable with `cat` like everything else
  wf writes. Concurrency is the lease discipline that already exists:
  `wf run --max 3` touches three different files, and two writers on one
  task is the case leases were added for. Rejected: **SQLite** (a dependency
  and a schema story for what is a directory of small documents) and **one
  `tasks.json`** (turns three independent concurrent runs into contention on
  one file).

- **The branch is recorded by the run that created it,** as part of the
  `workspace` binding, alongside its path and repo. `WorktreeName` stays as
  the *naming* rule for new worktrees and stops being the *lookup* rule for
  existing ones.

## Non-goals

Unchanged from DESIGN.md, and worth restating because a task object is
exactly the thing that tempts each of them:

- **Not a workflow engine.** Bindings are a record of what happened, not a
  graph of what happens next. `NEXT` still materializes a sibling and still
  does not launch it.
- **Not a state machine beyond `WorkState`.** A binding's `state` is about
  that binding — live, disposed, merged, closed — never about the task.
- **No sync back from the tracker into the ledger.** Publication is one
  direction. A tracker field that disagrees with a binding does not
  overwrite it; the tracker wins on work state, the ledger wins on
  artifacts, and they are disjoint by construction.
- **Not an artifact store.** A binding is a reference plus a lifecycle,
  never content. Documents keep living in the vault, diffs in git.

## What this unlocks

Each of these is currently either impossible or a special case:

- `wf show <ref>` renders one object — checkout, branch, every run with its
  outcome, every PR with its state, every document, the review pane — instead
  of a hand-assembled subset.
- `wf gc`: bindings with lifecycle make "disposed worktrees still recorded",
  "branches with no task", and "dead review panes" queries rather than
  guesses.
- **PR completion.** A `pr` binding with a refreshable state lets wf close a
  task when its PRs merge — the one capability pi-tasks had that wf dropped.
- **Trackerless start.** Begin work, get a worktree and a session, file the
  issue later; the `queue` binding attaches to a task that already has a
  history.
- **Multi-host correctness**, by construction rather than by convention.
- Both clients — the pi extension and `obsidian-wf` — read one JSON shape
  instead of decoding metadata keys, which is what `--json` was supposed to
  buy them already.

## Open questions

Named rather than quietly decided, because each could go the other way:

- **Reconciliation.** A tracker row closed by a human while the ledger holds
  a live worktree. The disjointness rule above says the tracker wins on work
  state and the worktree is then garbage — but "closed remotely" is probably
  worth surfacing rather than collecting silently.
- **Retention.** Bindings for a task closed six months ago cost nothing to
  keep and something to read past. `review.json` caps at 50 refs; a task
  ledger probably wants a `wf gc --before` rather than a cap.
- **Whether `workflow` is a binding or a field.** It is one-per-run in
  practice, which argues for a field on the `session` binding; it is
  historically plural across re-runs, which argues for a binding. Leaning
  field-on-session, since a workflow is how a run was dispatched rather than
  a thing the task acquired.

## Build plan

Staged so that each stage is shippable and the risky one is last.

- [ ] **1 — The type, over today's storage.** `Bindings` with the envelope
      and kinds; `LoadBindings(task)` reading the current metadata keys;
      every ad-hoc `Meta[...]` read in `main.go`, `output.go`,
      `review.go`, `session.go` and `apply.go` moved behind it.
      `review.Inputs` becomes `Bindings`. Pure refactor, no format change,
      and the point at which the type earns its place or does not.
- [ ] **2 — Role names.** New key names carrying no product in them; read
      both, write new. Migration is one release of tolerant reads.
- [ ] **3 — The local ledger.** `~/.wf/tasks/<id>.json`, dual-written with
      tracker publication, read local-first. `review.json` folds into it.
      Record the branch. Fixes the multi-host clobber and the re-run
      overwriting a kept checkout.
- [ ] **4 — Standalone identity.** wf-minted ids, `wf task new`, adopt into
      a tracker, ref resolution across both id spaces. `wf review --pr`
      becomes a one-binding task.
- [ ] **5 — Lifecycle.** `wf gc`, PR state refresh, completion when every PR
      on a task has merged.

## Risks

- **A local store is a store.** It can be stale, corrupt, half-written, or
  deleted. Mitigation is the shape it already has in `review.json`: a
  missing or malformed file reads as empty rather than failing, and every
  binding is a *reference* to something independently verifiable — so a lost
  ledger costs history and convenience, never work. Nothing in the run loop
  may take a lifecycle decision from the ledger alone.
- **Two writers, again.** Publication to the tracker plus a local write is
  two writes per fact and they can diverge on a crash between them. The
  ledger is authoritative and republishing is idempotent, so the recovery is
  "publish again," not "reconcile."
- **Stage 1 could show the type is not worth it.** If collapsing the five
  readers into one saves nothing legible, that is the signal to stop at
  stage 2 and keep the metadata format — the multi-host bug is then worth
  fixing on its own, much more cheaply, by namespacing the machine-local
  keys per actor.

## Done means

`wf task new "fix the parser"` with no kata running produces a task with a
worktree and a session bound to it; `wf show` renders both; filing it into
kata later adds a row without disturbing either; and `wf review` picks what
to open by asking the task what it has rather than by rebuilding the answer
from six metadata reads and a guess at a branch name.
