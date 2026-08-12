# pi-tasks

An Obsidian plugin that binds [pi](https://github.com/badlogic/pi-mono)
coding-agent sessions to documents. A task document holds checkbox tasks;
each task can launch its own pi agent; every task and review document owns
a durable pi session that opens as a regular Obsidian tab; `%% @pi: ... %%`
comments in any bound document are resolved by its session; finished work
opens for review — documents in a review pane, code diffs in
[difit](https://github.com/yoshiko-pg/difit). The document is the durable
artifact — sessions are its working memory.

Forked from
[sigilmakes/obsidian-pi-plugin](https://github.com/sigilmakes/obsidian-pi-plugin)
(MIT). Upstream lives unmodified at `upstream/` as a git subtree (update
via `git subtree pull`); our code in `src/` imports its RPC client, stream
handler, renderer, and input components directly.

## Prerequisites

- **Desktop Obsidian** (the plugin spawns processes; mobile is not
  supported).
- **pi** installed and on your PATH:
  `npm i -g @mariozechner/pi-coding-agent`, then run `pi` once in a
  terminal to configure a provider/API key. If the binary lives elsewhere,
  set its path in the plugin settings.
- **node/npx** on your PATH (used to launch difit; `npx difit` downloads
  it on first use, or `npm i -g difit` to pin it).
- **A git-backed vault** if you want diff review: difit reviews git
  changes, so `git init` your vault (worth it for history anyway).
  Document review works without git.
- Optional: **skills** for the agents — e.g.
  `pi install https://github.com/ssinnott/skills-n-stuff` gives every
  session the drawer's skills. Pi loads skills relative to its cwd, which
  is the vault root.
- Optional: a Quarto plugin for Obsidian if your review documents are
  `.qmd` — the review pane opens any vault file, but rendering `.qmd`
  needs a plugin.

## Install the plugin

From this folder:

```
npm install
npm run build
mkdir -p "<your-vault>/.obsidian/plugins/pi-tasks"
cp manifest.json main.js styles.css "<your-vault>/.obsidian/plugins/pi-tasks/"
```

Then in Obsidian: Settings → Community plugins → enable **Pi Tasks**.
(`styles.css` at the repo root is a build artifact copied from
`upstream/styles.css`; upstream stays untouched.)

## First use (2 minutes)

1. Open any note, run the command **Open pi session for this note** — a
   chat tab opens, and the note gains a `pi-session` frontmatter line
   pointing at `.pi-sessions/<id>.jsonl` in your vault.
2. Make a task doc:
   ```
   - [ ] Summarize [[meeting-notes]] into a decision list
   ```
   Cursor on the line → **Launch pi agent for task line**. The line gets
   ⏳ and a hidden session comment; a new tab does the work; on completion
   the box checks itself and a `([[...|review]])` link appears if the
   agent produced a document.
3. Add `%% @pi: tighten this paragraph %%` anywhere in a bound note →
   **Resolve @pi comments in this note** — the bound session applies the
   instruction and deletes the marker.
4. Review: a finished task that produced a document opens it in the
   review pane (comment there with `@pi` markers). A task that shipped
   PRs goes into review (🔃) with one child line per PR — **Open review
   for task under cursor** launches difit over the PR's branch range in
   the repo where the work happened; comment on diff lines, hit **Copy
   All Prompt**, and paste into the task's session tab. **Update PR
   statuses in this note** sweeps PR states via `gh` and checks the task
   off once everything is merged. Diff review is only for PRs — documents
   are never reviewed as diffs.

## Commands

- **Open pi session for this note** — resolve-or-create the note's
  `pi-session` binding and open its chat tab.
- **Launch pi agent for task line** — fresh agent for the checkbox task
  under the cursor; status is written back to the line (⏳ → checked on
  DONE, ❌ on BLOCKED).
- **Open session for task under cursor** — reopen the session recorded on
  a task line (hidden `%% pi:session=... %%` comment).
- **Open review for task under cursor** — a document review link opens in
  the review pane; a task with PRs opens difit over the PR's branches in
  its work repo (falling back to the PR page).
- **Resolve @pi comments in this note** — send the resolution instruction
  to the note's bound session.
- **Review changes in difit** — manual difit launch, any time.
- **Update PR statuses in this note** — sweep PR child lines via the gh
  CLI; tasks complete when all their PRs merge.
- **Switch model (active pi tab)** — change the model for the focused
  session.

## Settings

- **pi binary path** (default `pi`)
- **difit command** (default `npx difit`)
- **gh path** (default `gh`) — used for PR status refresh and resolving
  PR branches for difit review.

## How it fits together

Task lines carry their own wiring — a visible review link and a hidden
session comment — so the document, not the plugin, stores the
task↔session↔review relationships; everything survives sync, rename, and
restarts. Sessions are `.jsonl` files under `.pi-sessions/`; closing a tab
kills its pi process, reopening resumes from the file. Agents declare
their outcome: `DONE — review: <path>` for a document, `PR:` lines plus a
`REPO:` line for pull requests (tracked as child lines to merge), plain
DONE otherwise. Diff review is only for PRs. Design decisions and their
rejected alternatives are in [DESIGN.md](DESIGN.md).
