# pi-tasks

Forked from [sigilmakes/obsidian-pi-plugin](https://github.com/sigilmakes/obsidian-pi-plugin)
(MIT). Upstream lives unmodified at `upstream/` as a git subtree; update it
via `git subtree pull`. Our code (in `src/`) imports upstream's RPC client,
stream handler, renderer, and input components directly.

An Obsidian plugin that binds pi coding-agent sessions to documents. A task
document holds checkbox tasks; each task can launch its own pi agent; every
task and review document owns a durable pi session that opens as a regular
Obsidian tab; `%% @pi: ... %%` comments in any bound document are resolved by
its session. The document is the durable artifact — sessions are its working
memory.

## Commands

- **Open pi session for this note** — resolves (or creates) the note's
  `pi-session` frontmatter binding and opens its chat tab.
- **Launch pi agent for task line** — starts a fresh agent for the checkbox
  task under the cursor; the line is marked ⏳ while running and checked /
  marked ❌ when the agent reports DONE / BLOCKED.
- **Resolve @pi comments in this note** — sends the comment-resolution
  instruction to the note's bound session.

Sessions are `.jsonl` files under `.pi-sessions/` in the vault, so they
travel with it. Closing a tab kills its pi process; reopening resumes from
the session file.

## Install

1. Requires [pi](https://github.com/badlogic/pi-mono) installed and on your
   PATH (or set the binary path in plugin settings). Desktop only.
2. `npm install && npm run build` in this folder.
3. Copy `manifest.json`, `main.js`, and `styles.css` into
   `<vault>/.obsidian/plugins/pi-tasks/` and enable the plugin.

`styles.css` at the repo root is a build artifact copied from
`upstream/styles.css` (upstream stays untouched).
