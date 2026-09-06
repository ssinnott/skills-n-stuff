---
status: proposed
---

# Folding pi-tasks into the wf task layer

A proposal covering the Obsidian side of
[wf/DESIGN-task.md](../wf/DESIGN-task.md). That document makes the task
wf's own object; this one works out what happens to the two plugins once
it is, and the short answer is that pi-tasks loses its runtime and keeps
its ideas.

## Goal

One Obsidian plugin over the wf task model: documents are where work is
written down, a board and a queue are where it is seen, a task note is
where one piece of work shows everything bound to it. No agent runtime in
the vault at all — pi becomes a value in wf's runner field and nothing in
Obsidian knows the name.

## What pi-tasks got right, and what it should lose

The plugin is 7,183 lines. The split is not close:

| | lines | |
| :-- | --: | :-- |
| `upstream/` vendored console | 4,330 | goes |
| `src/view.ts` doc-bound chat view | 788 | goes |
| `src/docbind.ts` | 399 | **keep** (zero Obsidian imports, 293 lines of tests) |
| `src/board.ts` | 366 | **keep** (already "stores nothing of its own") |
| `src/main.ts` | 832 | splits — doc wiring keeps, pi/gh/difit wiring goes |

5,118 lines — 71% of the plugin — is a chat client, before counting
main.ts's session spawning, profile resolution, model switching, difit
process management and `gh` shelling.

**The console is not merely over the top; the design change dissolves its
rationale.** pi-tasks vendored a chat client for exactly one reason:
*doc-bound sessions*, where a document owns a session through `pi-session`
frontmatter. Its own DESIGN.md draws the line there — "Not a general chat
client. Upstream's obsidian-pi-plugin covers free-floating chat; this
plugin exists for doc-bound sessions." Under the task model the **run**
owns the session and the **task** owns the run. A document is no longer
anything's session-owner; it is a view of a task. Remove the premise and
that non-goal swallows the plugin's entire console.

What pi-tasks got right is all in the two files that survive: work is
written as prose with checkboxes in documents you already keep, status is
visible where you wrote it, a board is a projection rather than a store,
and behavior lives in skills rather than plugin code. None of that needed
pi. It needed a task object, which is what it was approximating.

## Decisions

- **The two plugins merge into one.** They were kept apart for a stated
  reason — "different sources of truth (a document's checkbox lines vs.
  kata's ledger), different dependency lists, so either installs without
  the other." Dropping pi, `gh` and difit-spawning from pi-tasks leaves it
  needing only the `wf` binary, which *is* obsidian-wf's dependency list,
  and the wf task is the single source of truth both were approximating.
  Both stated reasons are gone, so the split costs a duplicate
  implementation and buys nothing.

  Four duplications collapse:

  - **Two difit owners → one.** pi-tasks manages its own `difitProcess`
    with a `difitCommand` setting; wf already owns difit's lifecycle with
    `review.json`, one-pane-at-a-time and port memory. The plugin should
    frame a URL wf hands it, which is what obsidian-wf already does.
  - **Two PR-status implementations → one.** pi-tasks shells `gh` per PR
    child; the task proposal puts PR state on a `pr` binding wf refreshes.
    One of these has a place to store the answer.
  - **Two outcome parsers → one.** `docbind.parseOutcome` in TypeScript
    and `internal/wf/outcome.go` in Go implement the same six verbs. The
    Go one is the waist; the plugin should render JSON, not re-parse
    transcripts.
  - **Two task models → one.** Checkbox lines and kata rows become wf
    tasks, seen through two surfaces.

- **Documents are the capture surface; task notes are the view surface.**
  This keeps pi-tasks' best idea without keeping its ambiguity. A checkbox
  line in any document is where you *write work down* — cheap, in prose,
  next to your thinking. Promoting one (`wf task new`, from a cursor
  command or a board button) mints a wf task and leaves a marker binding
  the line to it. The task note described in the wf proposal is where that
  task's runs and bindings *render*. Rejected: keeping the checkbox line
  as the task record — that is the second source of truth the whole
  proposal exists to remove, and it is why `parseOutcome`, `findPRChildren`
  and `appendDocChildren` had to exist in the plugin at all.

- **The board projects wf tasks, not task lines.** `board.ts` keeps its
  shape — columns, cards, card actions routing through shared entry
  points, nothing stored — and changes its input from `docbind.scanTasks`
  over the vault to `wf --json`. Unpromoted checkbox lines can surface as
  a capture column, which is the honest rendering of "written down, not
  yet real work."

- **`docbind.ts` survives as the document layer, minus the protocol.**
  Frontmatter, task-line parsing, markers, wikilink extraction and status
  write-back stay: they are pure string functions with no Obsidian imports
  and a real test suite, and something has to read documents. What leaves
  is everything that was re-implementing wf in TypeScript — `parseOutcome`,
  `findPRChildren`/`appendPRChildren`, `findDocChildren`/`appendDocChildren`,
  `appendNextTasks`. Those become renderings of the managed block.

- **The vocabulary drops the product name, matching the CLI.** `pi-session`
  frontmatter becomes `wf-task`; `%% pi:session=… %%` markers become
  `%% wf:task=… %%`; `%% @pi: … %%` comments become `%% @agent: … %%`.
  Same rule as the CLI's: nothing user-facing names the runner.

- **`@agent` comments become a workflow dispatch, not a chat message.**
  Today a `%% @pi: … %%` marker is routed as an instruction into the
  document's live session — which needs a live session, which needs the
  console. Instead the comment seeds a run: `wf run <ref> --workflow
  comment-resolution`, with the marker text and its surrounding context as
  the prompt. This is better than a workaround. Resolution goes through
  the same run record as everything else, so it is attributable and
  reviewable instead of vanishing into a chat scrollback, and it works on
  a document whose task has no session open.

- **Chat leaves the vault, and the honest cost is named.** Steering a run
  is `wf attach <ref>` in a terminal — already the documented path, and
  runner-agnostic. For anyone who wants a pi chat UI inside Obsidian,
  upstream's `sigilmakes/obsidian-pi-plugin` is the thing pi-tasks forked
  in the first place, installed alongside; un-forking is a return, not a
  loss, and it stops this repo maintaining 4,330 vendored lines it did not
  write.

  The cost is real and worth stating plainly: someone who lived in the
  doc-bound chat tab loses in-app steering and gets a terminal. That is a
  genuine ergonomic downgrade for that workflow. It is accepted because
  the alternative is a vendored chat client, a second runner integration,
  and a session-ownership model that contradicts the task model — a large
  standing cost for one convenience.

## Naming

`obsidian-tasks` is unavailable in practice: there is a widely-installed
community plugin by that exact name (obsidian-tasks-group), and taking it
would be confusing at best. Worth checking before committing to anything,
but the merged plugin is most honestly named for what it fronts — keeping
`obsidian-wf` and letting "wf" mean the whole task layer, with the display
name saying tasks rather than queues.

## What the merged plugin is

One plugin, needing only the `wf` binary on PATH:

- **Board** — wf tasks as cards, columns by work state, card actions
  dispatching runs and opening reviews.
- **Queue pane** — escalations above ready work, as today.
- **Task note** — the managed block from the wf proposal: runs, and what
  each produced, with produced documents as wikilinks.
- **Documents** — checkbox capture, promotion to a task, status
  write-back, `@agent` comments.
- **Frames** — the tracker's own UI for the `queue` binding, difit for
  whatever `wf review` resolved. Both framed, neither owned.

## Non-goals

- **No agent runtime in the vault.** No RPC client, no process
  supervision, no model selection, no profile resolution. The plugin
  spawns `wf` and renders JSON.
- **Not a chat client**, now without the doc-bound exception that made the
  old plugin one anyway.
- **No plugin-side state**, which both plugins already claim and one of
  them already achieves.
- **Not a replacement for a task-management plugin.** This tracks agent
  work with bindings, not personal to-dos with recurrence and queries.

## Open questions

- **Does the vendored fork die or move?** Deleting it is cleanest and
  upstream covers the use case. Keeping the multi-tab and session-binding
  work alive as patches offered upstream — which pi-tasks' DESIGN.md
  already promised to do — is more generous and more work. Leaning: offer
  the patches, delete the fork here either way.
- **Do promoted checkbox lines stay in their document?** A line bound to a
  task could keep rendering status inline (nice: the doc stays live) or
  become a plain wikilink to the task note (nice: one place to look).
  Leaning inline status, since writing work down next to your thinking and
  then having it go inert is the thing that makes doc-based task systems
  feel dead.
- **Is the capture column worth it**, or should unpromoted lines simply
  not be on the board? Depends whether promotion feels like a step or a
  chore in practice.
- **What happens to existing vaults** carrying `pi-session` frontmatter
  and `%% pi:… %%` markers. A read-both migration is easy; whether to
  write a `wf migrate-vault` or just tolerate old keys indefinitely is not
  obvious at this scale.

## Build plan

Ordered so the plugin keeps working throughout; the deletion is last, not
first.

- [ ] **1 — Land the wf side.** Stages 1–5 of
      [wf/DESIGN-task.md](../wf/DESIGN-task.md): the binding type, runs,
      the local ledger, `wf task new` / `wf run <ref> --workflow`, and
      `wf note sync`. Nothing here is buildable before that exists.
- [ ] **2 — Board over wf.** Repoint `board.ts` from `docbind.scanTasks`
      to `wf --json` inside obsidian-wf, keeping its columns and card
      actions. First point at which one plugin shows both.
- [ ] **3 — Move the document layer.** `docbind.ts` and its tests into
      obsidian-wf, minus the outcome protocol and the PR/doc child
      machinery; add promote-line-to-task and the `wf-task` marker.
- [ ] **4 — Rename the vocabulary.** `wf-task` frontmatter, `%% wf:… %%`
      markers, `@agent` comments, with tolerant reads of the old names.
- [ ] **5 — `@agent` as dispatch.** Comment resolution seeds a run instead
      of messaging a session.
- [ ] **6 — Delete the runtime.** `upstream/`, `src/view.ts`, and main.ts's
      pi, gh and difit-spawning wiring. Archive obsidian-pi-tasks with a
      README pointing at obsidian-wf and at upstream for chat.

## Risks

- **This is a lot of deletion to do on a proposal.** Stage 1 gates
  everything, and if the wf task object does not survive contact with
  implementation, stages 2–6 are wrong in a way that is expensive to
  discover late. Mitigation is the ordering: the plugin keeps working
  until stage 6, and stage 6 is reversible from git for as long as anyone
  cares.
- **Losing in-app steering may hurt more than it reads on paper.** The
  mitigation — upstream's plugin, installed alongside — is untested in
  this configuration, and "install a second plugin" is a real cost. If it
  turns out that doc-bound steering was the point of the whole thing, the
  answer is not to re-vendor a console but to ask why a run needs steering
  that often.
- **A vault mid-migration has both vocabularies.** Tolerant reads cover
  it, but a board that silently drops tasks it cannot parse would be a bad
  failure. It should show them as unreadable rather than omit them.

## Done means

One plugin, one binary dependency, no vendored runtime. Writing `- [ ] fix
the parser` in a daily note and promoting it produces a wf task; the board
shows it; dispatching it runs a workflow; its worktree, session, PR and
produced documents render in its task note; and nothing anywhere in the
vault mentions pi.
