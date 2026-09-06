---
status: proposed
---

# Deleting pi-tasks, keeping what it proved

pi-tasks is removed from this repo. It was the first attempt at the idea
[wf/DESIGN-task.md](../wf/DESIGN-task.md) is now building properly — work
bound to its artifacts — and it got a surprising amount right while
hanging all of it off the wrong object. This document is the harvest: the
claims worth carrying, the mistakes worth remembering, and what
obsidian-wf builds instead. It exists so the deletion loses the thinking
and not just the code.

The code is in git history at `obsidian-pi-tasks/` if a detail is ever
needed. Nothing below depends on reading it.

## Why delete rather than migrate

The plugin was 7,183 lines. 5,118 of those — the vendored `upstream/`
console and `src/view.ts` — existed for one reason: *doc-bound sessions*,
where a document owns a pi session through `pi-session` frontmatter. Under
the task object the **run** owns the session and the **task** owns the run,
so a document is no longer anything's session-owner and that entire
apparatus loses its premise. pi-tasks' own DESIGN.md had already drawn the
line — "Not a general chat client. Upstream's obsidian-pi-plugin covers
free-floating chat; this plugin exists for doc-bound sessions" — and
removing the premise lets its own non-goal swallow it.

What remained was `docbind.ts` (399 lines) and `board.ts` (366). Porting
them was the earlier plan and it was wrong: roughly half of `docbind.ts`
is `parseOutcome`, `findPRChildren`/`appendPRChildren`,
`findDocChildren`/`appendDocChildren` and `appendNextTasks` — a
TypeScript re-implementation of the outcome protocol and the binding model
that wf owns in Go. Porting it would carry structure built for the model
being abandoned. The ~200 lines that genuinely survive are worth
rewriting against the task object, not transplanting.

So: delete now, unblocked by the wf schedule, with the ideas written down
here. A half-dead plugin waiting five stages for its own removal is worse
than either keeping it or killing it.

## Claims worth carrying

Stated as claims rather than features, because the features were shaped by
a model we are replacing and the claims are not.

- **A board is a projection, not a store.** pi-tasks rejected board-side
  persistence twice — once as a session-browser sidebar, once as
  drag-to-move columns — on the grounds that either makes a second,
  ephemeral copy of what the documents already say. It held, and it is why
  its board never went stale.
- **Documents are where work gets written down.** A checkbox line in prose,
  next to the thinking that produced it, is the cheapest possible capture
  and the reason doc-based systems feel good to start work in.
- **Status must be written back where the task was written.** A document
  system whose status lives somewhere else goes inert, and an inert
  document stops being worth opening. This is the argument for the managed
  block rendering into the note rather than living only in `wf show`.
- **Wiring belongs in the document, not in plugin state keyed by
  file+line.** pi-tasks rejected the latter explicitly — it breaks on edits
  and is invisible to the human — and used hidden `%% pi:session=… %%`
  markers instead. The managed block is the same idea widened from a line
  to a region.
- **A produced document is a fact, not an obligation.** DOC artifacts
  linked as plain wikilinks rather than checkboxes, so nothing gates on
  them and the board ignores them. Subtle, and right: an artifact is
  evidence that work happened, not work outstanding.
- **A filed issue never gates completion**, because creating the issue was
  the task. Already inherited by wf.
- **Follow-on work materializes but does not auto-launch.** Silent fan-out
  with no review gate was rejected here first. Already inherited by wf.
- **Review is polymorphic and agent-declared.** The agent names its primary
  artifact; a document opens as a document, code goes to a diff viewer.
  wf's review ladder is the direct descendant, with the guessing removed.
- **Tasks outlive their sessions.** An agent that ships PRs is not done
  when its process exits; the task stays in review until every PR merges.
  This is the one capability wf does not have yet, and it is why the task
  proposal gives `pr` bindings a refreshable state.
- **Behavior lives in skills, not plugin code.** pi-tasks built two
  workflow starter skills and then deleted them, on the grounds that which
  workflows an agent sees belongs to the user's configuration rather than
  this repo. wf's "a workflow is dispatch wiring, not expertise" is the
  same decision.
- **One writer per document.** The plugin wrote frontmatter, agents wrote
  bodies. Any scheme with two writers into one markdown file needs a rule
  this explicit.

## Mistakes worth remembering

- **The task concept was hung off the runtime.** Everything above was
  reachable only through pi: a task needed a session, a session needed a
  live process, a process needed a chat tab. The object that deserved to be
  first-class was never modelled at all.
- **Vendoring 4,330 lines to obtain one feature.** Session binding was the
  goal; a whole chat client was the price. Forking upstream to get a
  constructor parameter is a trade that looks cheap on day one and is a
  maintenance burden forever.
- **The protocol was implemented on both sides of the boundary.**
  `docbind.parseOutcome` in TypeScript and `internal/wf/outcome.go` in Go
  parse the same six verbs. Whichever side owns a protocol, the other side
  should render its output, not re-derive it.
- **Two owners for one external process.** pi-tasks supervised its own
  difit; wf supervises difit too, with port memory and a
  one-pane-at-a-time rule. Nobody decided this — it happened because two
  components both needed the same tool and neither knew the other existed.
- **A lesson can be written down and then not applied.** pi-tasks rejected
  path-derived session names because they break on rename. wf then derived
  worktree *branch* names from task titles, which breaks on rename in
  exactly the same way. Writing the rule down did not make it transfer.

## What obsidian-wf builds instead

Not a port. The surfaces below are what the claims above imply once the
task object exists, and each is written against `wf --json`:

- **Board** — wf tasks as cards, columns by work state, card actions
  dispatching runs and opening reviews. `board.ts`'s shape was right;
  its input changes from scanning the vault to reading the CLI.
- **Task note** — the managed block from the wf proposal: runs, and what
  each produced, with produced documents as wikilinks.
- **Documents as capture** — checkbox lines, promotion to a wf task
  (`wf task new`) leaving a `%% wf:task=… %%` marker, and status written
  back inline so the document stays live.
- **`@agent` comments dispatch a run** rather than messaging a live
  session: `wf run <ref> --workflow comment-resolution`, seeded with the
  marker text and its context. Better than the original — it needs no open
  session, works on any document, and is recorded like every other run
  instead of vanishing into a chat scrollback.
- **Frames**, unchanged: the tracker's own UI for the `queue` binding,
  difit for whatever `wf review` resolved. Both framed, neither owned.

The plugin needs only the `wf` binary. No RPC client, no process
supervision, no model selection, no profile resolution, no `gh`.

## What is lost, plainly

- **In-app steering.** Someone who lived in the doc-bound chat tab now uses
  `wf attach <ref>` in a terminal. That is a genuine downgrade for that
  habit. Anyone wanting a pi chat UI in Obsidian can install upstream's
  `sigilmakes/obsidian-pi-plugin` — the plugin pi-tasks forked — alongside.
- **The `pi-tasks-setup` skill**, deleted with it: every check it ran
  (`pi --version`, `gh auth status`, difit via npx, vault-is-a-git-repo,
  plugin files installed, profile dirs) targeted this plugin's toolchain.
  Its *method* is worth reviving as a `wf-setup` skill — prove each link
  with a real command, keep the output as evidence, and never claim what a
  shell cannot verify (it was careful to say plugin-enablement can only be
  checked by the human). Not written yet.
- **Vaults using the old vocabulary.** Any vault carrying `pi-session`
  frontmatter or `%% pi:… %%` markers has no upgrade path in this repo.
  Given the plugin never had a release, this is very likely a population of
  one, who is reading this.
- **The upstream patches never offered.** pi-tasks' DESIGN promised to
  offer its multi-tab and session-binding changes back to
  sigilmakes/obsidian-pi-plugin as graftable patches. That was never done,
  and deleting the fork ends the possibility here. Worth doing as a
  courtesy from the git history if the multi-tab work has value upstream.

## Build plan

- [x] Harvest: this document.
- [x] Delete `obsidian-pi-tasks/` and `skills/pi-tasks-setup/`; clear the
      references in the root README, obsidian-wf, and wf's README.
- [x] Land the wf side: [wf/DESIGN-task.md](../wf/DESIGN-task.md) — all six
      stages are built. The binding type, role-named keys, the local ledger,
      runs as the middle layer, standalone identity (`wf task new`,
      `wf task adopt`, `wf run <ref> --workflow`), `wf note sync`, `wf gc`
      and `gh`-backed PR refresh. Everything below is now unblocked.
      One caveat the plugin will meet: `wf run` refuses a task with no
      tracker row, so "capture, promote, dispatch" is only end-to-end for a
      task that has been adopted into the queue.
- [ ] Board over `wf --json`, with columns and card actions.
- [ ] Document capture: checkbox lines, promotion, `wf:task` markers,
      inline status write-back.
- [ ] `@agent` comment dispatch.
- [ ] A `wf-setup` skill, carrying the deleted one's method.

## Risks

- **The harvest is only as good as this document.** Once the code is gone,
  a claim recorded imprecisely here is a claim lost. Mitigation is that the
  code stays in git history and this file names the files worth reading.
- **Rewriting rather than porting can lose detail the tests held.**
  `docbind.ts` had 293 lines of tests encoding real edge cases in task-line
  and frontmatter parsing. Whatever replaces it should be written against
  the same cases, read out of history rather than rediscovered.
- **Deleting before the replacement exists leaves a gap.** Accepted
  deliberately: the plugin was not released, the wf work is the real
  dependency, and keeping a runtime nobody wants alive to avoid a gap is
  how the vendored console survived this long in the first place.
