---
status: in-progress
supersedes: parts of DESIGN-task.md (named per stage)
---

# Slimming wf back to a wrapper

A plan, not a redesign. [DESIGN.md](DESIGN.md) says what wf is: one CLI
that reads ready work from a queue, leases it, gives it a worktree, runs an
agent, parses the outcome verbs, and writes the result back. kata is the
queue, pi is the runner, git is the workspace. Everything below is about
getting the code back to that sentence.

## Where the weight is

Non-test Go, by what it is for:

| Concern | Lines | Share |
| :-- | --: | --: |
| The wrapper: kata, pi, worktrees, workflows, supervisor, outcomes, leases, `ready` / `run` / `attach` | ~4,100 | 37% |
| The ledger and what grew on it: store, bindings, two id spaces, `task`, `show`, `gc`, `pr refresh`, `note sync` | ~5,600 | 50% |
| Review: difit lifecycle, port memory, localStorage harvest | ~1,400 | 13% |

Tests are another 10,900 lines and comments are 27% of source. The wrapper
is a third of the binary.

The root is one decision. DESIGN.md's non-goals say "no second source of
truth". [DESIGN-task.md](DESIGN-task.md) then added a local ledger with
wf-minted ids, and the ledger fixed two real bugs (a laptop and a desktop
clobbering each other's paths, and a task that could not be re-run while its
kept checkout was on disk). But it also brought a second id space, a dual
write of every fact, a resolver across both spaces, a note renderer, a
GitHub client, a garbage collector, and the compatibility shims to keep the
old shapes readable. The feature that motivated standalone identity, running
work with no tracker row, does not work and is documented as structural.

## Rules for deciding what stays

Each stage below applies these. They are the test for any future feature
too.

1. **wf owns the loop and the outcome protocol. Nothing else.** Ready, lease,
   workspace, run, apply, release, and the verbs an agent ends with.
2. **A fact lives in exactly one place.** Shareable facts (repo, PRs, filed
   issues, produced documents, state, lease, attention) live on the tracker.
   Machine-local facts (checkouts, session files, review panes) live on the
   machine. Nothing is written to both, and nothing is read back from a
   place it was only published to.
3. **If a specialized tool answers the question, wf calls it and does not
   cache the answer.** Branch and worktree existence is git's. PR state is
   GitHub's. Queue order, priority and open/closed are kata's.
4. **Rendering belongs to the surface that displays it.** wf emits JSON. The
   Obsidian plugin renders notes. The pi extension renders lines.
5. **No compatibility shims until there is a second user.** One release of
   tolerant reads for a tool with one operator is a permanent tax. Migrate
   by hand once, delete the old path.
6. **A feature with no consumer is deleted, not finished.** Half-built state
   is worse than absent state.

## The one decision: which id is the task's

Everything else is deletion. This is the fork.

**Option A: no ledger.** Every binding goes back to kata metadata.
Machine-local keys are namespaced per actor (`wf.session@wf-laptop`) so two
hosts stop clobbering each other. Re-runs keep a history key the way
`wf.session_history` already does. Deletes about 5,600 lines. Loses run
provenance and per-run workspace bindings, and puts absolute paths back into
shared state, which DESIGN-task.md argued against on grounds that still hold.

**Option B: a ledger keyed by the tracker's id, holding only what the tracker
cannot.** One id space, kata's ULID. The record under `~/.wf/tasks/<ulid>.json`
holds runs and machine-local bindings: workspace, session, review pane.
Nothing else. Shareable facts stay on kata and are read from kata. `wf show`
joins the two at read time. Deletes about 4,200 lines and keeps the two
fixes the ledger was built for.

**Recommendation: B.** It keeps the multi-host and re-run fixes, which were
real, and removes everything that came from wf minting its own identity,
which was not. Option A remains the fallback: if after stage 5 the ledger
is still not earning its lines, deleting it is a smaller step from B than
from today.

Under B the ledger stops being "wf's own object" and becomes a per-host cache
of facts the tracker has no field for. That is the honest description of
what it was ever needed for.

## Stages

Ordered so each is shippable on its own, cheap deletions first, the
structural change in the middle once the code it would have had to migrate
is already gone. Sizes are non-test lines removed, rounded.

### Stage 0: baseline

Record the numbers so the plan can be checked against them.

- `go test ./...` green, `go vet ./...` clean, and the per-package line
  counts above pasted into the PR description that opens the work.
- Baseline: commit `66497df` on `claude/cli-bloat-analysis-0vpxcp` is the last
  full-featured build; all 15 packages pass `go test ./...` there.
- Tag the commit before stage 1 as the last full-featured build, so a note
  or a PR record can be recovered from it if the migration in stage 5 loses
  something.

### Stage 1: delete PR refresh

Rule 6. `wf pr refresh` asks `gh` whether PRs merged and records the answer
on the ledger. Nothing consumes it: the task is reported "completable" but
not closed, and the tracker's copy is never updated.

- **Delete:** `internal/gh/` (423), `cmd/wf/pr.go` (185),
  `internal/wf/completion.go` (84), the `GhBin` config field and `GH_BIN`
  override, and the PR-state section of README.
- **Delete tests:** `gh_test.go`, `refresh_test.go`, `pr_test.go`,
  `completion_test.go`.
- **Keep:** `review.PRHandle` and the PR URL parse, which `wf review --pr`
  uses.
- **Clients:** none call it.
- **If PR-merge completion is ever wanted:** it belongs in the supervisor as
  a close with evidence through the `Queue`, not as a state cache. `gh`
  comes back then, with a consumer.
- **Done means:** ~700 lines gone, `go test ./...` green, `wf` has no `pr`
  command.

### Stage 2: move note rendering into the Obsidian plugin

Rule 4. About 1,500 lines of Go exist to render a task into a managed block
in a vault note, including a vault index, filename slugging, rename
detection, atomic writes, relative ages, and self-link suppression. The
plugin already reads `wf show --json`, already runs on note open, and
already handles renames.

- **Delete:** `internal/notesync/` (651), `internal/note/taskblock/` (529),
  `cmd/wf/note.go` except `cmdBind` (~180 of 244), the `NoteDir` config
  field, `wf note sync`, `wf note sync --all`, and the `noteSync` JSON
  shapes.
- **Delete tests:** `notesync_test.go`, `block_test.go`, `note_test.go`.
- **Keep:** `internal/note/frontmatter.go` (76) for `wf bind`.
- **Change `wf bind`:** it writes the note's frontmatter and the tracker's
  `wf.doc` key, and nothing else. Two writes, one per side of the id pair.
- **Add to the plugin:** a `taskblock.ts` of roughly 150 lines that renders
  the `record` section of `wf show --json` into the same `%% wf:begin %%`
  and `%% wf:end %%` region, called on note open and after a dispatch.
  Existing notes keep working because the delimiters do not change. Port
  the rendering rules from `block.go`: runs newest first, bindings ranked by
  kind, machine-local bindings annotated with their host, produced documents
  as wikilinks. Skip the write when the rendered block is byte-identical.
- **Not ported:** note creation. A human creates a note and binds it. `wf
  note sync <ref>` creating one was the "lazy" answer to a question that
  goes away when the plugin owns the note.
- **Done means:** ~1,400 lines of Go gone, the plugin renders the block for
  an existing bound note, and `wf` has no `note` command.

### Stage 3: shrink gc to the ledger

Rule 3. `wf gc` sweeps worktrees, branches, pids and panes across 830 lines.
Git already reports missing worktrees and lists branches. What only the
ledger can answer is "which recorded bindings point at nothing".

- **Delete:** `sweepWorktreeRoot`, `sweepBranches`, `GitBranches`,
  `sweepPane`, the `Repo` and `WorktreeRoot` options, `window.go` and
  `--before`, the three-mode `--fix` / `--delete` split.
- **Keep:** one pass over records marking a workspace or session binding
  `missing` when its path is gone on this host, and reporting it. Foreign
  host bindings stay untouched, as today.
- **Replace `--delete`:** a record is droppable when every local binding is
  missing or disposed. Print them; `--delete` removes them. No retention
  clock, because the ledger under Option B is a cache and a dropped record
  costs nothing the tracker does not still hold.
- **Point at git for the rest:** README says `git worktree prune` and
  `git branch --list 'wf/*'` for the artifacts, one line each.
- **Done means:** `internal/gc/` under 150 lines, `cmd/wf/gc.go` under 60,
  `gc_test.go` rewritten against the smaller surface.

### Stage 4: one review record

Rule 2. The live viewer is recorded twice: in `review.json` with the per-ref
port map, and as a review binding on the task record. DESIGN-task.md said the
file would fold into the ledger. It did not.

- **Keep `review.json`.** It is 147 lines, it is what `wf review --pr <url>`
  needs when there is no task at all, and the port map is keyed by ref, which
  works for a URL handle as well as a task.
- **Delete:** `KindReview`, `recordReviewPane`, `retireReviewPanes` in
  `cmd/wf/review.go`, and the review lines in `wf show`. `taskForPR`, which
  files a one-binding ledger task for a pasted URL, goes with them: `--pr`
  returns to the ad hoc entry point it was, which DESIGN.md already accepted.
- **Done means:** one file records the pane, `wf review --pr` needs no
  ledger, `review_test.go` and `reviewpr_test.go` trimmed to match.

### Stage 5: one id space, no dual write

The structural stage, and the one the others cleared the ground for. This is
Option B.

**Identity.**

- **Delete:** `internal/wf/id.go` (110), `cmd/wf/task.go` (299) and the
  `task new` / `task adopt` / `task list` commands, `Record.Handle`,
  `Record.Title`, `Record.Name`, `KindQueue`, `Bindings.Note`, the
  `Resolve` passes in `store/file.go` (`matchExact` / `matchFold` /
  `matchPartial` / `describe`), `AmbiguousError`, `needsQueue`,
  `dispatchRef`, `recordFromTask`, and the `resolved` struct.
- **Replace with:** `app.resolve(ref)` calls `kata show <ref>` and takes the
  ULID it returns. The ledger is then `Load(ulid)`, a missing record being an
  empty one. kata already accepts its own short ids, so every ref form a
  human types today still works, minus the wf handle, which no longer exists.
- **`wf task list`** is `kata list`. README says so.
- **JSON:** `wf show --json` emits one object: the tracker row's fields at
  the top level as `jsonTask` has them, plus `runs` and `local` (the
  machine-local bindings) from the ledger. The `record` key and the
  `taskJSON` compatibility shape go away. Both clients read `task` today and
  neither reads `record`, so the change to them is one field rename each.

**One place per fact.**

- **Sessions leave the tracker.** `BindSession` and `FinishSession` stop
  writing `wf.session`, `wf.session_id`, `wf.workspace` and
  `wf.session_history` to kata. The session and workspace bindings are
  written once, to the ledger, by `recordSession` and `recordWorkspace`,
  which already do it. `sessionFromMeta` and its second lookup go.
- **Shareable facts leave the ledger.** `runBindings` produces nothing for
  PRs, issues, documents or repos. `recordRunFacts` keeps publishing those
  to kata exactly as it does, and `LoadBindings` keeps reading them back
  from kata, because kata is now their only home and reading your own
  published key back from the one store that holds it is not the sync-back
  the design refused. `Run` keeps workflow, profile, model, outcome and
  timestamps; provenance of which run opened which PR is the one thing this
  loses, and nothing consumed it.
- **Delete the legacy readers:** the `pi.*` keys and `ObsidianNoteKey`, the
  `Legacy*` constants, `BindingFromMeta`, `HistoryFromMeta`, and the
  session half of `load.go`.
- **`wf attach`** reads the session from the ledger only, host-checked as
  today. A task run on another host says so.
- **`wf review`** builds its ladder from `LoadBindings(kata row)` for the PR
  and document rungs and from the ledger for the workspace rungs. `Resolve`
  is unchanged; only the caller that assembles the bindings changes.
- **Supervisor:** `taskRef` and `ledgerRef` collapse to the ULID the task
  already carries. `identify` goes.

**Migration.** A one-off command, `wf migrate-ledger`, that renames each
`~/.wf/tasks/<wfid>.json` to its queue binding's ULID and strips the fields
that no longer exist, then deletes itself in the following commit. Given one
operator and a tagged pre-stage-1 build, `rm -r ~/.wf/tasks` is an acceptable
alternative and README says which to pick.

- **Done means:** `wf run neck`, `wf show neck`, `wf attach neck` and `wf
  review neck` work end to end with a fresh ledger; `grep -r 'pi\.' internal`
  finds no key names; `store/file.go` is under 250 lines; `wf` has no `task`
  command; the supervisor integration tests pass against real kata.

### Stage 6: flag parsing and the command table

Small, but it is where the surface is declared, so it comes after the surface
has shrunk.

- **Replace** the argv scanner (`flagValue`, `hasFlag`, `positionals`,
  `knownFlags`) with one `flag.FlagSet` per subcommand from the standard
  library. `--json` and `--config` are registered on every set.
- **Drop `--ref`** on `run`; the positional is the form. The Obsidian client
  passes the positional instead, one line in `wf.ts`.
- **The command table** after stages 1 to 5:

  ```
  wf ready [--limit N]
  wf show <ref>
  wf escalations
  wf workflows
  wf run [<ref>] [--once] [--max N] [--repo P] [--workflow W]
  wf attach <ref>
  wf bind <ref> <note.md>
  wf ui [<ref>]
  wf review <ref> | --pr <url> [--repo P] | <ref> --stop
  wf review comment <ref> [--format difit]
  wf gc [--delete]
  ```

  Eleven leaves, ten of them called by a client. `gc` is the one that is
  not, and it is fifty lines.
- **Done means:** `main.go` under 300 lines, `wf help` prints the table
  above, every flag typo is an error instead of a silent positional.

### Stage 7: docs and comments

- **README:** usage section is the table from stage 6. The "how the pieces
  bind" section becomes a paragraph: shareable facts on the tracker,
  machine-local facts in `~/.wf/tasks`, nothing in both.
- **DESIGN-task.md:** mark `status: superseded`, keep the "What is actually
  missing today" and the multi-host and re-run findings, which were correct,
  and add a short "what was rolled back and why" pointing here. Do not
  delete it; the reasoning for the ledger's existence is the reasoning for
  keeping the half that stays.
- **DESIGN.md:** the non-goals get one line: "no second id space".
- **Comments:** a package comment states what the package does in one
  paragraph and links the design doc for why. Inline comments that argue a
  decision move to the design doc or go. Target is comments under 15% of
  source, which is where `kata.go` and `workflow.go` already sit.
- **Done means:** a new reader can find every command in the README and
  every design decision in one of two design docs, and neither doc
  describes code that no longer exists.

### Optional stage 8: drop the difit harvest

Not in the plan by default, because the Obsidian plugin actively uses it.
Named so the criterion is written down.

`wf review comment --format difit` parses difit's browser localStorage in a
shape "verified by observation, not documented anywhere", with four tolerant
input forms, and the plugin reaches into an Electron webview to extract it.
The paste path through difit's own "Copy All Prompt" button does the same job
with a human click.

**Drop it when** difit ships a comments endpoint or changes its storage key
shape, whichever comes first. On the first, replace the harvest with a fetch.
On the second, do not chase it: remove `ParseDifitStore`, `FormatDifitThreads`,
`--format difit`, and `difitframe.ts`'s harvest, about 250 lines of Go and
100 of TypeScript, and keep the paste path.

## Progress

Non-test Go lines after each stage's commit landed:

| Stage | Commit | Non-test lines |
| :-- | :-- | --: |
| 1 | `972d920` | 10,399 |
| 2 | `839a46f` | 9,120 |
| 3 | `51386a1` | 8,509 |
| 4 | `2024ccc` | 8,391 |
| 5, identity | `c1324d1` | 7,638 |
| 5, no dual write | `4fcc86e` | 7,227 |
| 6 | in progress | — |
| 7 | in progress | — |

Where the outcome differed from the plan text above:

- **Stage 2's** Obsidian renderer port is ~460 lines, not the ~150
  estimated, because it pins the Go fixtures exactly rather than
  approximating the rendering rules.
- **Stage 4** kept `review.json` and dropped the ledger binding, as
  planned.
- **Stage 5's** `migrate-ledger` command is hidden rather than deleted
  the commit after; it stays until the next release.
- **Stage 5** also dropped `session` and `cwd` from `ready`,
  `escalations` and `run --json`; they are on `show` only.

## Projected size

Non-test lines, before and after stages 1 to 7:

| Package | Before | After |
| :-- | --: | --: |
| `cmd/wf` | 2,466 | ~1,100 |
| `internal/wf` | 2,368 | ~1,400 |
| `internal/store` | 630 | ~250 |
| `internal/supervisor` | 834 | ~700 |
| `internal/review` | 1,053 | ~950 |
| `internal/gc` | 677 | ~150 |
| `internal/gh` | 423 | 0 |
| `internal/notesync` + `note/taskblock` | 1,180 | 0 |
| `internal/note` | 76 | 76 |
| unchanged: kata, runner, workspace, workflow, config | 1,394 | 1,394 |
| **Total** | **11,101** | **~6,000** |

Roughly 45% smaller, with the wrapper going from a third of the binary to
two thirds. Tests should fall by a similar fraction since most of what goes
is tested in proportion.

## Risks

- **Stage 5 is the only stage that can lose data.** Everything before it
  deletes code whose state either lives elsewhere (notes, git) or was never
  consumed (PR state). Stage 5 rekeys the ledger. The tag from stage 0 and
  the migrate command cover it; the plan does not proceed to stage 6 until
  `wf attach` reaches a session recorded before the migration.
- **The Obsidian block renderer is a port, and ports drift.** Pin it with
  the same fixture `block_test.go` uses today, moved to the plugin as a
  test, before deleting the Go side.
- **Reading published keys back from kata** (stage 5) is the one place the
  "never read back" rule is relaxed. It is safe because kata is now those
  facts' only store, so there is nothing to disagree with. If a second
  writer for those keys ever appears, that is the moment to revisit.
- **Option A may still be right.** If after stage 5 the ledger holds only
  sessions and workspaces and `wf show` is the only reader, the per-actor
  namespacing in kata metadata is a smaller thing to maintain than a store
  package. Decide with the numbers in hand, not now.

## Done means

`wf run --once` picks the top ready issue off kata, leases it, builds a
worktree, runs pi in it, and closes the issue with evidence the agent
produced. `wf attach` drops you into that session. `wf review` opens the
diff. Every fact about the task is in one place, every command in the help
text has a client that calls it or fits on one screen, and the binary is
half its current size with the same integration tests passing.
