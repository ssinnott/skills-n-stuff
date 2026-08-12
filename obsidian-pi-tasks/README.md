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
- **Update PR statuses in this note** — sweep PR and Issue child lines
  via the gh CLI; tasks complete when all their PRs merge (issues are
  tracked but never block completion).
- **Switch model (active pi tab)** — change the model for the focused
  session.

## Settings

- **pi binary path** (default `pi`)
- **difit command** (default `npx difit`)
- **gh path** (default `gh`) — used for PR status refresh and resolving
  PR branches for difit review.
- **Profiles** — `name = config dir` lines; each directory is passed to
  that session's pi as `PI_CODING_AGENT_DIR`.
- **Default profile** — used when a task or note names none.

## Workflows

The plugin has no workflow engine — a workflow is a skill or slash
command named in the task text, and the seed prompt tells the agent to
follow it. What unifies them is the outcome protocol every agent speaks
on its final lines:

- `DONE — review: <path>` — the deliverable is a document; it opens in
  the review pane.
- `PR: <url> — <title>` (+ `REPO: <path>`) — work shipped as pull
  requests; the task enters review (🔃) with one child line per PR and
  completes only when they all merge.
- `ISSUE: <url> — <title>` — an issue the agent filed. Tracked as a
  child line, box checked when it closes, but it never blocks the task:
  creating the issue *was* the work.
- `NEXT: <task text>` — a follow-up the outcome calls for. It
  materializes as a fresh sibling task line carrying the artifact's
  links, so one workflow's result is cached in the doc as the next
  workflow's launchable starting point.
- `DONE` / `BLOCKED` — direct completion or a stuck report.

That's the mixin: an issue-research task ends with `ISSUE:` + `NEXT: fix
it via /plan-to-pr`, you launch the materialized task when ready, and it
ends with `PR:` lines that track to merged. Build-failure triage, snyk
updates, and support tickets all fit the same loop — each is a task line
naming its workflow, each leaves artifacts and optionally the next task
behind. Chains stay human-launched: nothing auto-fires, the doc shows
every step.

No workflow is baked into the plugin — a workflow is just a skill the
agent can see, named in the task text. Which skills a session sees is a
pi loading question, answered per task by profiles (below): defining a
workflow is one `SKILL.md` that describes the judgment and ends with
the protocol verbs above — nothing needs registering with the plugin.
The drawer ships **pi-tasks-setup**, a meta skill that verifies and
repairs this whole toolchain.

## Profiles

pi has no native profile concept, but it has the seam for one:
`PI_CODING_AGENT_DIR` overrides its entire config directory (default
`~/.pi/agent`) — and that directory's `settings.json` owns the
installed-packages list, so a directory *is* a profile: its own
packages, skills, extensions, prompts.

The plugin makes profiles per-task. Name them in settings
(`research = ~/.pi-profiles/research`, one per line), then pick one:

- per task line, with a hidden `%% pi:profile=research %%` marker;
- per document, with `pi-profile: research` in frontmatter;
- globally, via the default-profile setting (empty = pi's own config).

Precedence is task line → frontmatter → default. Launching a task
records the resolved profile on its line, so reopening the session
later spawns the same kind of agent. To create a profile, make the
directory and `PI_CODING_AGENT_DIR=~/.pi-profiles/research pi install
<packages>` into it — each profile is set up exactly like a normal pi
install. Unknown profile names warn and fall back to pi's default
config rather than silently running with the wrong toolset.

## How it fits together

Task lines carry their own wiring — a visible review link and a hidden
session comment — so the document, not the plugin, stores the
task↔session↔review relationships; everything survives sync, rename, and
restarts. Sessions are `.jsonl` files under `.pi-sessions/`; closing a tab
kills its pi process, reopening resumes from the file. Agents declare
their outcome via the protocol above; the plugin only routes and records.
Diff review is only for PRs. Design decisions and their rejected
alternatives are in [DESIGN.md](DESIGN.md).
