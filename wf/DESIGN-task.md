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

A task is an **identity, a history of runs, and the typed bindings those
runs produced**: worktrees, agent sessions, pull requests, documents, filed
issues, review panes, and the tracker row itself — each recorded with its
own lifecycle and its own provenance rather than as a loose string.

The task is then the centralized view of one piece of work: `wf show` and an
Obsidian note are two renderings of the same object, and every other surface
(the queue pane, the kata frame, difit) is a view of one of its bindings.

Standalone means two things, both load-bearing:

- **Nothing in the vocabulary names a product.** A session's runner is `pi`
  as a *value*; a queue's backend is `kata` as a *value*; a document's store
  is `vault` as a *value*. Today those names are keys — `pi.session`,
  `obsidian.note` — which is the same mistake pi-tasks made by hanging its
  task concept off pi, one level down. The generalization matters as much as
  the instance: binding the task to Obsidian would be the same error again,
  and that constrains the storage decision below.
- **A task exists whether or not a tracker row does.** Starting work and
  filing an issue are different acts, and today they have to be the same one.

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
  a laptop and a desktop against one kata silently overwrite each other's
  session and checkout, and `wf attach` hands you a path that does not exist
  on the machine you are sitting at.
- **Bindings are single-valued where the work is plural.** One
  `obsidian.note`: the first produced document wins and the rest survive
  only as prose in a comment. One `pi.workspace`: a re-run overwrites the
  checkout of the escalated run a human was on their way to inspect — the
  checkout wf deliberately *kept* for exactly that purpose.
- **Bindings carry no lifecycle and no provenance.** Nothing records that a
  worktree was disposed; `wf review` finds out by `os.Stat`. Nothing records
  that a PR merged, so wf cannot do what pi-tasks could — complete a task
  when all of its PRs land. And nothing records *which run* produced which
  artifact, so a task with two runs has one flat pile of results.
- **The branch is derived, not recorded.** `workspace.WorktreeName` builds
  it from the task's *title*. Rename a task in kata and rung 3 of the review
  ladder looks for a branch that no longer answers to that name, while the
  real branch sits in the repo unreferenced. A fact a run established is
  being recomputed from a field a human is free to edit.
- **Work with no tracker row cannot be a task at all.** Not a hypothesis:
  `wf review --pr` exists as a documented second entry point that "bypasses
  the ladder and the queue entirely" precisely because a PR a human opened
  by hand has nowhere to hang.

## Decisions

### The shape

- **A task is an identity plus typed, plural, stateful bindings.** One
  envelope — `{kind, ref, label, state, at, via, payload}` — over a closed
  set of kinds: `queue`, `repo`, `workspace`, `session`, `pr`, `doc`,
  `issue`, `review`, `task`. Every kind is a list; "the current one" is a
  query over it (the newest live binding of that kind), not a separate key.
  Rejected: a free-form key/value bag with better names — that is what
  exists, and names are not the problem; the absence of a type every reader
  shares is.

- **The vocabulary names roles, not products.** `session.runner = "pi"`,
  `queue.backend = "kata"`, `doc.store = "vault"`. This is the whole content
  of "standalone": a second runner or a second prose layer becomes a value in
  an existing field instead of a parallel set of keys with a parallel set of
  readers. Rejected: keeping `pi.*` and `obsidian.*` as key names for
  compatibility — the migration is one release of tolerant reads, and
  carrying the names forward carries the coupling forward with them.

- **Runs are the middle layer, and they are what makes a workflow's output
  bind back.** A task holds *runs*; a run holds the bindings it produced.
  A run records the workflow, profile and model it was dispatched with, when
  it started and ended, and how it settled — and every binding it creates
  carries `via: <run-id>`.

  This is the piece the flat model cannot express, and it buys four things at
  once. **Provenance**: which run opened this PR, which run built this
  checkout. **Re-run safety**: a second run gets its *own* worktree binding
  and the first is marked superseded rather than clobbered, which is the
  direct fix for a re-run destroying an escalated run's evidence.
  **Comparison**: two runs of the same workflow under different models are
  two rows with two sets of artifacts, which is exactly the question
  "was opus worth it here" needs. And **the view**: a task rendered as
  runs-with-their-artifacts is the centralized picture, where a flat pile of
  metadata keys is not.

  Bindings that predate any run — an adopted PR, a note bound by hand — carry
  `via: null` and read as the task's own, which is the honest record.
  Rejected: modelling a run as just another binding kind. It is the only
  thing that *produces* bindings, and flattening the producer in among the
  produced is what loses the provenance edge.

- **`wf run <ref> --workflow <name>` is the invocation surface, and it is the
  only one.** Today a workflow is *selected* implicitly — `wf run` takes the
  top of the queue and resolves a workflow from `wf.workflow` metadata or a
  label — and there is no way to say "run this recipe on this task." Naming
  the task and the workflow explicitly is the verb every client wants: a
  button on an Obsidian task note, `/wf run neck plan-to-pr` in the pi
  extension, a bare shell invocation. All three shell out to the same
  command, because DESIGN.md already decided the CLI is the only
  orchestration surface and the clients hold no state.

  Implicit selection stays exactly as it is for queue-driven dispatch
  (`wf run --once`, `--max N`): a queue that routes by label must keep
  working without anyone naming anything. The explicit form is an override,
  and it records the workflow on the *run*, so a task run twice under two
  workflows has an honest history instead of a single overwritten field.

- **The branch is recorded by the run that created it,** as part of its
  `workspace` binding, alongside path and repo. `WorktreeName` stays the
  *naming* rule for new worktrees and stops being the *lookup* rule for
  existing ones.

- **The review ladder becomes a query.** `Resolve`'s four hardcoded rungs
  over hand-assembled inputs become: order the task's bindings by strength
  (`pr` > live `workspace` > `workspace` branch > `doc`) and take the first
  live one. `os.Stat` moves from being a rung's private check to being how a
  `workspace` binding's state is refreshed. `wf review --pr <url>` stops
  being a bypass and becomes an ordinary task carrying exactly one `pr`
  binding — same code path, one binding shorter.

- **`NEXT` still creates a sibling task, and the link becomes a binding.**
  Follow-on work has its own lifecycle, so it stays its own task rather than
  a queued second run. What changes is that the parent/child edge is a
  `task` binding on both ends instead of a `wf.origin` metadata string, which
  is what lets a chain render as a chain — in `wf show`, and natively in
  Obsidian's graph once tasks have notes.

### Where it lives

- **The ledger is local; the tracker keeps work state. Split by durability,
  not by convenience.** A binding points at something that lives on one
  machine — a checkout, a session file, a browser origin — and is exactly as
  durable as the thing it points at. A worktree path is meaningless on
  another host, so co-locating the record with the artifact is not a
  compromise, it is correctness. Title, body, priority, labels, open/closed
  and queue order stay reads against the tracker; wf still stores no issue
  state, and DESIGN.md's "no second source of truth" holds for every fact it
  was written about.

  This is less a new store than the one that already exists growing up:
  `review.json` is *already* a local per-ref binding ledger — a live viewer
  plus a ref→port map, written beside wf's config, capped and evicted —
  built for exactly this reason and currently the only binding wf keeps
  honestly.

- **Shareable bindings are published to the tracker, one way, never read
  back.** PRs, documents, filed issues and `wf.state` keep being written as
  metadata exactly as today, so kata's CLI, TUI and web UI keep showing what
  they show now. Publication is derived output. This is already wf's posture
  on `wf.pr` — written as wf's own memory, deliberately never read back,
  because kata's evidence format is undocumented — the decision just names it
  and applies it uniformly.

- **wf mints the id; the tracker row is a binding.** `wf task new "fix the
  parser"` works with no kata running. Adopting a tracker row *adds* a
  `queue` binding rather than replacing identity, and `wf show <ref>`
  resolves a ref against wf's id, its short handle, the tracker id or the
  tracker's short id. Rejected: **reusing kata's ULID as wf's primary key** —
  it reads as free (no mapping table) and costs the two things this proposal
  is for: identity is unavailable until a row exists, and the backend is
  re-bound to wf at the one place hardest to change later.

- **One JSON file per task under `~/.wf/tasks/`, with SQLite available later
  as a derived index rather than as the record.** Three candidates were
  weighed; this section is longer than the others because the choice is the
  one that constrains everything downstream.

  **JSON files — chosen.** The access pattern is "load one task, all of it,"
  which is a document read. Concurrency is one file per task under the lease
  discipline that already exists, so `wf run --max 3` touches three
  different files and never contends. It is inspectable with `cat` and `jq`,
  matching every other thing wf writes — `review.json`, session `.jsonl`,
  workflow markdown. And it holds the line the README states outright: *no
  dependencies beyond the standard library.*

  **SQLite — deferred, and the deferral has a named trigger.** Its real
  advantage is cross-task queries: every task with an open PR, every worktree
  older than thirty days, which task owns this branch. Over a few hundred
  tasks a scan-and-parse of the directory answers those in single-digit
  milliseconds, so the advantage is not yet an advantage. The cost is
  immediate and concrete: `mattn/go-sqlite3` needs cgo, which gives up the
  clean static cross-compile that DESIGN.md picked Go *for*, and
  `modernc.org/sqlite` is pure Go but is an enormous transpiled-C dependency
  to take on for a directory of small documents. The escape hatch is that
  this is not a one-way door — an index is a *cache over the files*, so
  `~/.wf/index.db` can appear later, rebuildable and deletable, without the
  record format changing at all. Revisit when a ledger scan actually shows up
  in a profile.

  **Everything is an Obsidian doc — rejected as the store, adopted as the
  view.** This is the most attractive of the three and deserves the real
  argument, because what it offers is genuine: if a task *is* a note, the
  visualization question answers itself (you open it), backlinks and graph
  and Dataview and search and mobile sync all come free, the doc↔task id
  pair stops needing to exist because a produced document is just a
  wikilink, and it is continuous with pi-tasks' own "the document is the
  durable artifact."

  Four things kill it as the record. **It re-creates the exact bug this
  proposal exists to fix**: a vault syncs across devices, so putting a
  laptop-only `~/.wf/worktrees/...` path into a note propagates a
  machine-local fact to every device — worse than the kata version, not
  better. **Concurrency**: pi-tasks already needed "single writer per doc"
  as an explicit mitigation, and this makes `wf` a third writer, from
  outside Obsidian, at arbitrary times, into a format with no atomic
  compare-and-swap. **Parsing**: bindings are structured and nested, and
  DESIGN.md's flat-frontmatter rule exists because Go's standard library has
  no YAML parser — encoding a binding list flatly gets you `wf-pr-1-url`,
  `wf-pr-1-state`, which is worse than the JSON it is avoiding.
  **And it makes the vault mandatory**: a task could not exist without
  Obsidian, which is the same coupling as binding the task to pi, one
  substitution away.

  So the vault gets the *rendering*, described below, and the plugin keeps
  the property its README already claims — it stores nothing.

- **A variant worth naming: the vault as the tracker.** Dropping kata and
  letting notes hold title, body and state is a coherent architecture, and
  the honest reason not to is that kata's queue semantics — ready, priority,
  dependencies, and above all the evidence-gated close — are, per DESIGN.md,
  "the main reason to be on this tracker at all." The good news is that this
  proposal does not foreclose it: a vault-backed tracker is a `Queue`
  adapter, which is precisely what that seam was cut for, and the note
  rendering below is the same either way.

## How it looks

### CLI

`wf show <ref>` becomes the whole object, grouped by run, and `--json` is the
same shape both clients read:

```
neck   Add the parser                                       running
  queue     kata 01M1S…                                     open · p1
  repo      ~/code/app

  run 1     plan-to-pr · coding · sonnet-5   2h ago          escalated
    workspace  ~/.wf/worktrees/neck-add-parser              live · wf/neck-add-parser
    session    ~/.wf/sessions/neck-a1b2.jsonl               wf attach neck
    doc        Research/parser-plan.md

  run 2     plan-to-pr · coding · opus-5     12m ago         done
    workspace  ~/.wf/worktrees/neck-add-parser-2            live · wf/neck-add-parser-2
    pr         github.com/me/app#412                        open

  review    difit :4980                                     live
```

Everything else stays the verb it already is — `wf run`, `wf review`,
`wf attach`, `wf bind` — operating on a task that now has somewhere to put
what they produce.

### Obsidian

**The task note is the face; wf is still the record.** A note carries
`wf-task: <id>` in frontmatter — durable, human-visible, survives rename,
and exactly the join `kata-issue` already is. Bindings render into a
*managed block* the plugin rewrites from `wf show --json`:

```markdown
---
wf-task: 01M1S…
---
# Add the parser

My own notes here, never touched.

%% wf:begin %%
- **run 2** · plan-to-pr · opus-5 · done · 12m ago
  - PR [#412](https://github.com/me/app/pull/412) — open
  - worktree `neck-add-parser-2` — live *(wf-laptop)*
- **run 1** · plan-to-pr · sonnet-5 · escalated · 2h ago
  - [[Research/parser-plan]]
%% wf:end %%
```

Three properties make this work rather than churn:

- **Everything outside the block is yours**, which is pi-tasks' hidden-marker
  pattern (`%% pi:session=… %%`) generalized from one line to a region, and
  keeps the single-writer rule intact — the plugin owns the block, you own
  the note.
- **Machine-local bindings render as labels, annotated with their host.** A
  worktree shows as live *on wf-laptop*; on your phone that reads as a fact
  about another machine instead of a dead path. This is the vault-as-store
  failure turned into a feature, because the projection can say what the
  store could not.
- **Produced documents render as wikilinks**, so Obsidian's backlinks give
  you doc→task navigation for free and the graph shows the chain of `NEXT`
  siblings natively.

The existing panes then stop competing to be the primary view and become
views of individual bindings: the queue pane lists tasks and opens their
notes, the kata frame shows the `queue` binding's discussion, difit shows
whatever `wf review` resolved.

**obsidian-wf is the only plugin.** obsidian-pi-tasks — the first attempt
at this idea, with the task hung off a pi session instead of standing on
its own — has been deleted, and what it proved is harvested in
[obsidian-wf/DESIGN-tasks.md](../obsidian-wf/DESIGN-tasks.md). obsidian-wf
takes over its board and its document layer, written against `wf --json`
rather than ported. The task note above is what it renders.

## Non-goals

Unchanged from DESIGN.md, and worth restating because a task object is
exactly the thing that tempts each of them:

- **Not a workflow engine.** Runs are a record of what happened, not a graph
  of what happens next. `NEXT` still materializes a sibling and still does
  not launch it; chains stay human-started.
- **Not a state machine beyond `WorkState`.** A binding's `state` is about
  that binding — live, disposed, merged, closed — never about the task.
- **No sync back from the tracker into the ledger.** Publication is one
  direction. A tracker field that disagrees with a binding does not overwrite
  it; the tracker wins on work state, the ledger wins on artifacts, and they
  are disjoint by construction.
- **Not an artifact store.** A binding is a reference plus a lifecycle, never
  content. Documents keep living in the vault, diffs in git.
- **The plugin still stores nothing.** The managed block is regenerated
  output; deleting it loses nothing.

## What this unlocks

Each of these is currently either impossible or a special case:

- `wf show <ref>` renders one object instead of a hand-assembled subset, and
  an Obsidian note renders the same object without a second implementation.
- **Provenance**: which run produced which artifact, answerable at all.
- **Re-runs stop destroying evidence**, because a second run gets its own
  workspace binding instead of overwriting the first.
- `wf gc`: bindings with lifecycle make "disposed worktrees still recorded",
  "branches with no task", and "dead review panes" queries rather than
  guesses.
- **PR completion.** A `pr` binding with a refreshable state lets wf close a
  task when its PRs merge — the one capability pi-tasks had that wf dropped.
- **Trackerless start.** Begin work, get a worktree and a session, file the
  issue later; the `queue` binding attaches to a task that already has a
  history.
- **Multi-host correctness**, by construction rather than by convention.
- Both clients read one JSON shape instead of decoding metadata keys, which
  is what `--json` was supposed to buy them already.

## Open questions

Named rather than quietly decided, because each could go the other way:

- **Who creates the task note, and when.** Eagerly on task creation (every
  task is a note, the vault fills with stubs) or lazily on first open or
  first produced document (fewer notes, but "open this task" sometimes has
  to create one). Leaning lazy, with `wf note sync <ref>` as the explicit
  verb.
- **Reconciliation.** A tracker row closed by a human while the ledger holds
  a live worktree. The disjointness rule says the tracker wins on work state
  and the worktree is then garbage — but "closed remotely" is probably worth
  surfacing rather than collecting silently.
- **Retention.** Bindings for a task closed six months ago cost nothing to
  keep and something to read past. `review.json` caps at 50 refs; a task
  ledger probably wants `wf gc --before` rather than a cap.
- **Whether a run's workflow can change mid-flight.** A task re-dispatched
  under a different workflow is a new run, which is settled. A run that
  *escalates* and is resumed is less clear: same run continued, or a new one
  with a `resumed-from` edge? Leaning new run, since the session binding is
  already per-spawn.

## Build plan

Staged so that each stage is shippable and the risky one is last.

- [ ] **1 — The type, over today's storage.** `Bindings` with the envelope
      and kinds; `LoadBindings(task)` reading the current metadata keys;
      every ad-hoc `Meta[...]` read in `main.go`, `output.go`, `review.go`,
      `session.go` and `apply.go` moved behind it. `review.Inputs` becomes
      `Bindings`. Pure refactor, no format change, and the point at which
      the type earns its place or does not.
- [ ] **2 — Role names.** New key names carrying no product in them; read
      both, write new. Migration is one release of tolerant reads.
- [ ] **3 — The local ledger, and runs.** `~/.wf/tasks/<id>.json` behind a
      narrow `Store` interface, dual-written with tracker publication, read
      local-first. `review.json` folds into it. Runs become the middle layer
      and `Apply` writes bindings tagged `via: <run-id>` instead of flat
      keys. Record the branch. Fixes the multi-host clobber and the re-run
      overwriting a kept checkout.
- [ ] **4 — Standalone identity and explicit dispatch.** wf-minted ids,
      `wf task new`, adopt into a tracker, ref resolution across both id
      spaces, `wf run <ref> --workflow <name>`. `wf review --pr` becomes a
      one-binding task.
- [ ] **5 — The note projection.** `wf note sync <ref>` writing the managed
      block; the plugin calling it on open and after dispatch; wikilinks for
      produced docs; host annotation on machine-local bindings.
- [ ] **6 — Lifecycle.** `wf gc`, PR state refresh, completion when every PR
      on a task has merged.

## Risks

- **A local store is a store.** It can be stale, corrupt, half-written or
  deleted. Mitigation is that every binding is a *reference* to something
  independently verifiable, so a lost ledger costs history and convenience,
  never work, and nothing in the run loop may take a lifecycle decision from
  the ledger alone.

  An earlier draft of this section cited `review.json` as the precedent for
  "a missing or malformed file reads as empty rather than failing." That was
  wrong about the existing code: `review.LoadState` tolerates a *missing*
  file and returns an error on a *malformed* one. The intent was right and
  the citation was not, so the ledger implements the intent and splits it by
  what the caller can do about it — `List` skips an unreadable file and names
  it, because one corrupt record must not cost its siblings, while `Load`
  reports the parse error, because there is no sibling to salvage and
  "no such task" would be a lie.
- **The ledger has no read-modify-write primitive, and stage 3 needs one.**
  "Apply writes bindings tagged `via: <run-id>`" is inherently
  load-then-mutate-then-save, and the five-verb `Store` interface makes that
  a lost-update race across the gap. The per-id lock inside `FileStore` does
  not close it — it serializes each `Save`, not a `Load`/`Save` pair. Either
  stage 3 leans entirely on the existing lease in `internal/wf/lease.go` for
  serialization, or `Store` grows `Update(id string, fn func(*wf.Record)
  error) error`. Leaning on the lease alone is not enough: `wf bind` and a
  running dispatch both write bindings, and only one of them takes a lease.

- **Two writers, again.** Publication to the tracker plus a local write is
  two writes per fact and they can diverge on a crash between them. The
  ledger is authoritative and republishing is idempotent, so the recovery is
  "publish again," not "reconcile."
- **The managed block is a third writer into the vault**, alongside the
  plugin and any agent working in it. Mitigation is that it is a *region*
  with explicit delimiters and regenerated wholesale, never merged — but a
  hand-edit inside the block is lost, and the delimiters need to survive
  Obsidian reformatting the file.
- **Stage 1 could show the type is not worth it.** If collapsing the five
  readers into one saves nothing legible, that is the signal to stop at
  stage 2 and keep the metadata format — the multi-host bug is then worth
  fixing on its own, much more cheaply, by namespacing the machine-local
  keys per actor.

## Done means

`wf task new "fix the parser"` with no kata running produces a task; `wf run
<ref> --workflow plan-to-pr` gives it a worktree, a session and a run record
that everything it produced hangs off; `wf show` renders that as one object
and an Obsidian note renders the same object without a second
implementation; filing it into kata later adds a row without disturbing any
of it; and `wf review` picks what to open by asking the task what it has
rather than by rebuilding the answer from six metadata reads and a guess at
a branch name.
