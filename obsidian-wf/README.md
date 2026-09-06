# wf Agent Queue

An Obsidian plugin that fronts the [wf](../wf) agent work queue: a queue
pane listing what is ready and what needs you, a framed pane showing
[kata](https://www.katatracker.com)'s own UI for the task under point, and a
framed pane showing [difit](https://github.com/yoshiko-pg/difit)'s diff
review UI for the task under review. Dispatching a task runs a wf workflow —
a canned recipe naming a pi profile, a model, a prompt, and resources —
against the tracker; artifacts the run produces (documents, pull requests,
issues) land back on the task and, for documents, in this vault. Reviewing a
task hands the diff to difit — with any agent findings wf already seeded as
comments — so the loop is a handoff, not just a viewer.

Deliberately separate from
[obsidian-pi-tasks](../obsidian-pi-tasks): that plugin binds pi sessions to
*documents* you write tasks into, and needs pi, difit, and gh. This plugin
binds agent runs to *tracker issues* dispatched by wf, and needs only the
`wf` binary — kata and pi sit behind it. Different sources of truth (a
document's checkbox lines vs. kata's ledger), different dependency lists,
so either installs without the other. The two interoperate only through one
frontmatter key, described below — there is no shared runtime state.

Everything here is a projection: the plugin stores nothing itself. Every
row in the queue comes from `wf --json`, every action writes back through
`wf`, and the plugin can be closed and reopened without losing anything.

## Prerequisites

- **Desktop Obsidian** (the plugin spawns the `wf` binary; mobile is not
  supported).
- **[wf](../wf)** built and on your PATH (`go build -o wf ./cmd/wf` from
  that directory, or point the plugin at the binary in settings), with a
  config at `~/.wf/config.json` naming your repo, vault, and workflows. See
  [`wf/README.md`](../wf/README.md).
- **kata** running (`kata mcp serve`, or however you run its daemon) — wf
  talks to it as the queue backend, and the framed pane embeds its web UI.
- **difit** reachable by whatever command wf is configured to run it with
  (e.g. `npx difit`) — wf starts it per review and reports back the URL;
  this plugin only frames whatever URL it is given. That command lives in
  wf's own config, not this plugin's settings.
- Everything wf itself needs to actually run a workflow — pi on PATH, pi
  profiles configured, a git remote if workflows open pull requests — lives
  in wf's own prerequisites, not this plugin's.

## Install the plugin

From this folder:

```
npm install
npm run build
mkdir -p "<your-vault>/.obsidian/plugins/wf-queue"
cp manifest.json main.js styles.css "<your-vault>/.obsidian/plugins/wf-queue/"
```

Then in Obsidian: Settings → Community plugins → enable **wf Agent Queue**.
(`styles.css` at the repo root is a build artifact — `src/styles.css`
copied — regenerate it with `npm run build` rather than editing it in
place.)

## First use

1. Settings → wf Agent Queue: set the **wf binary** path if it is not on
   PATH, and the **wf workspace** — the directory wf runs in, which is
   usually your repo (wherever `.kata.toml` and `~/.wf/config.json`'s repo
   live), not necessarily the vault.
2. Ribbon icon or **Open agent queue** — the queue pane opens on the left,
   showing escalations above ready work. **Dispatch next** runs the top of
   the queue; **Refresh** re-reads the tracker.
3. **Open kata UI pane** puts kata's own web UI in a pane on the right. A
   row's **Copy attach** button gives you `wf attach <ref>` for a terminal,
   or **Open note** if the task's run already bound a vault document.
4. Bind a note to a task ahead of dispatch with **Bind this note to a
   task** — pick from the ready queue; the note gets a `kata-issue`
   frontmatter line and dispatching that task shows its progress in the
   kata pane whenever the note is open (if **auto-open** is on) or via
   **Show this note's task in the kata pane**.
5. Once a task has something to look at, a row's **Review** button (or
   **Review this note's task**) runs `wf review`. For a PR, worktree, or
   branch this opens the difit pane pointed at wf's session; for a task
   whose review target is a document, the note opens instead — a document
   isn't a diff. Read the comments off, then use difit's own **Copy All
   Prompt** and paste it into the task's session tab; **Stop** in the difit
   pane's toolbar ends wf's difit process when you're done.

## Commands

- **Open agent queue** — open (or reveal) the queue pane.
- **Open kata UI pane** — open (or reveal) the framed kata UI.
- **Show this note's task in the kata pane** — resolve the active note's
  bound task and point the kata pane at it.
- **Bind this note to a task** — pick a ready task and write its ref into
  the note's `kata-issue` frontmatter.
- **Dispatch the next ready task** — run `wf run` once and report what
  happened (closed, or escalated with its reason).
- **Review this note's task** — run `wf review` for the active note's bound
  task: opens the difit pane on a diff, or the note itself when the review
  target is a document.

## Settings

- **wf binary** (default `wf`) — path to the CLI.
- **wf workspace** (default: vault root) — directory wf runs in, so it
  resolves the right `.kata.toml` and config.
- **Open the kata pane automatically** — when on, opening a bound note
  brings its task up in the kata pane beside it.

## How the id pair binds a note to a task

There is exactly one join, in both directions:

- `kata-issue: <ref>` in a note's frontmatter — the note-side half, read by
  this plugin to know which task a note belongs to.
- `obsidian.note: <vault path>` in the task's own metadata — the task-side
  half, written by wf itself when a `DOC:` artifact lands in the vault (see
  `wf`'s `ObsidianNoteKey`).

Binding a note through this plugin (or through a workflow's own DOC
artifact) writes both halves. Renaming a bound note updates the tracker
side automatically, so the join survives reorganizing the vault — that is
the one piece of state this plugin actively maintains rather than merely
projecting; everything else is read fresh from `wf --json` on every
refresh.

## Why a frame, not a native view

kata already has a UI. Reimplementing its board, filters, and detail view
natively in Obsidian would mean keeping a second renderer in sync with
kata's own — busywork with no payoff. Embedding it (a `<webview>` under
Electron, falling back to an `<iframe>`) gets the real thing for free,
at the cost of one thing an embedded page cannot do: tell Obsidian what
you clicked inside it. That is why selection lives in the queue pane, not
the frame — clicking a row in the queue pane is what points the frame at a
task, and opening a bound note is what points it via the id pair above.

The same argument, and the same shape of pane, covers difit: a diff-review
web app is not something worth rebuilding inside a plugin. Both framed panes
share their mechanics (`src/frame.ts`) and differ only in what points them
and how: the kata pane follows whatever note you're reading, because
watching a task's progress ambiently is the point of it; the difit pane
never does, because review is a deliberate act you start (a queue row's
**Review** button, or a command) — you would not want the pane to jump to a
different diff just because you opened a different note.

difit is a sharper case of "an embedded page cannot tell Obsidian what you
clicked" than kata is: kata at least has a queue this plugin can re-poll for
task state, but difit's line comments live only in the embedded page's own
browser localStorage, and difit exposes no API to read them back (`/api/diff`
exists; there is no `/api/comments`). So this plugin does not poll for
comments — there is nothing to poll — and never will. The loop is, and is
meant to stay, a clipboard action: difit's own **Copy All Prompt** button,
pasted into the task's session tab by hand. That is not a workaround; it is
the actual handoff this feature exists to make convenient — wf pre-seeding
agent findings as comments turns that same clipboard trip into a review of
what an agent already flagged, rather than a blank diff.

## How it fits together

`wf` owns the workflow: which pi profile and model to run, what prompt to
seed, where artifacts land, and the outcome protocol (`DONE` / `PR:` /
`ISSUE:` / `NEXT:` / `DOC:` / `REPO:`) that decides whether a run counts as
closed or needs a human. This plugin has none of that judgment — it lists
what `wf ready` and `wf escalations` report, dispatches through `wf run`,
and shows kata's own view of the task. If wf's protocol or workflow set
changes, this plugin does not, because it never encoded any of it itself.
