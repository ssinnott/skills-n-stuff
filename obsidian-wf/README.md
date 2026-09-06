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

This is the only Obsidian plugin here. `obsidian-pi-tasks` — which bound
pi sessions to documents — has been removed; what it proved, and what this
plugin takes over from it, is recorded in
[DESIGN-tasks.md](DESIGN-tasks.md). This plugin needs only the `wf` binary;
kata and pi sit behind it, and no agent runtime runs in the vault.

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
   isn't a diff. A PR that never went through the queue — one you opened
   yourself, or one that predates it — has no task to review through:
   **Review a pull request…** takes a URL directly (prefilled from a PR
   link under your cursor or selection, if there is one) instead.
6. Leave comments in difit as usual, then either **Save review comments to
   the task** to bank them without ending the session, or **Stop** in the
   difit pane's toolbar to pull them in and end wf's difit process in one
   action — see "How comments get out of difit" below for what that
   actually does and its one hard limitation. difit's own **Copy All
   Prompt** still works as a manual fallback, and is the only route when
   comment harvesting isn't available (see below).

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
- **Review a pull request…** — review a PR directly, with no task involved.
  Prefills the URL from a PR link on the current line or selection if there
  is one, otherwise prompts.
- **Save review comments to the task** — harvest whatever comments are
  sitting in the open difit pane and send them to the task, without
  stopping the review. Only works on the webview frame path (see below).

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
comments — there is nothing to poll — and never will.

That used to mean the loop ended at a clipboard action: difit's own **Copy
All Prompt** button, pasted into the task's session tab by hand. It still
does for the pi extension and any other non-Obsidian difit client, and it
still does here whenever the webview path isn't available (see below) — but
where the plugin renders difit in an Electron `<webview>` (`createFrame` in
`src/frame.ts`), it can *pull* the comment store back out on its own, even
though difit still has no way to *push* anything to the host. A `<webview>`
is a separate top-level browsing context that Electron's host process can
still reach into via `executeJavaScript` — so `DifitFrameView` runs a small
script inside the frame that enumerates difit's own `localStorage` (keys
shaped like `difit-storage-v1/<repo hash>/<base>-<target>`, one per
repo+commit-range difit has shown on that origin — observed by inspecting a
running difit page, since this isn't documented anywhere) and hands the lot
to `wf review comment <ref> --format difit --json` on stdin, which sorts out
what belongs to this ref.

A plain `<iframe>` — the fallback frame.ts uses when the `<webview>` tag
isn't available — cannot do this: it is a same-process but cross-origin
browsing context, and cross-origin `localStorage` is unreachable from the
host no matter what, by design, not by an omission this plugin could patch.
On that path, harvesting says so outright ("Can't harvest comments in this
build…") rather than silently coming back with nothing, and Copy All Prompt
remains the only route.

**Stop harvests before it kills, and that ordering is deliberate.**
localStorage is scoped per origin, and difit's origin is
`localhost:<port>` — a port wf is free to reuse or not on the next review of
the same ref — and, on top of that, difit's storage key itself embeds the
commit range under review, so a new commit on the same PR opens a *different*
key with no prior comments. Both are stranding vectors: comments left behind
when a viewer is killed can end up on an origin, or a key, that nothing ever
returns to. So **Stop** harvests the store, sends it to `wf review comment`,
and only *then* calls `wf review --stop`; if either the harvest or the send
fails, it does not stop anything — the viewer stays alive with the comments
still in the page, and you fall back to Copy All Prompt. **Save review
comments to the task** runs the same harvest-and-send without the stop, for
banking comments mid-review.

None of this changes what pre-seeding does: wf pre-seeding agent findings as
difit comments still turns your first look at a diff into a review of what
an agent already flagged rather than a blank one — harvesting is what gets
your own comments back out afterward, on top of that.

## How it fits together

`wf` owns the workflow: which pi profile and model to run, what prompt to
seed, where artifacts land, and the outcome protocol (`DONE` / `PR:` /
`ISSUE:` / `NEXT:` / `DOC:` / `REPO:`) that decides whether a run counts as
closed or needs a human. This plugin has none of that judgment — it lists
what `wf ready` and `wf escalations` report, dispatches through `wf run`,
and shows kata's own view of the task. If wf's protocol or workflow set
changes, this plugin does not, because it never encoded any of it itself.
