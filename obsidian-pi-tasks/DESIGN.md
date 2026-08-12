---
status: in-progress
pi-session: —
---

# pi-tasks: document-driven agent work in Obsidian

## Goal

An Obsidian plugin that binds pi sessions to documents. A task document
holds checkbox tasks; each task can launch its own pi agent; every task and
review document owns a durable pi session that opens as a regular Obsidian
tab; `@pi` comments in any bound document are resolved by its session. The
document is the durable artifact — sessions are its working memory.

## Non-goals

- No changes to pi itself. Everything rides on shipped seams: `--mode rpc`,
  `--session`, file editing, skill loading.
- No task board UI. The task document is the board; agents write status
  back to their own task line.
- Not a general chat client. Upstream's obsidian-pi-plugin covers
  free-floating chat; this plugin exists for doc-bound sessions.
- No MCP, no servers, no daemons.

## Decisions

- Sessions are tabs, one live `pi --mode rpc --session <id>` process per
  open chat tab; spawn on open, kill on close, resume from the session file
  on reopen. Rejected: a singleton chat view with session switching, and
  the session-browser sidebar as a task board — both make the session list
  a second, ephemeral copy of what the task doc already shows.
- Session identity lives in the document's frontmatter (`pi-session`).
  Rejected: path-derived session names — they break on file rename.
- Comments are Obsidian native comments carrying a marker:
  `%% @pi: <instruction> %%`. Text adjacency is the anchor; resolution
  deletes the marker. Rejected: a custom comment UI.
- Fork of sigilmakes/obsidian-pi-plugin (MIT, confirmed in package.json)
  rather than clean-room: upstream's RPC client, stream handler, and
  renderer are exactly the hard parts, and its PiConnection already takes
  extra spawn args, so `--session` slots in. The multi-tab change is an
  ownership move (connection per view instead of per plugin), not a
  rewrite. Vendored with attribution; graftable patches offered upstream.
  (Earlier draft chose clean-room when the license looked absent.)
- Behavior lives in drawer skills (task-doc, review-doc, comment
  resolution), not plugin code. The plugin's commands only route
  instructions into the right session; judgment stays in evaluable
  markdown (skills-n-stuff pattern).
- The task line is the hub: it carries a visible [[review]] link and a
  hidden `%% pi:session=... %%` comment, so the document itself stores the
  task↔session↔review wiring. Cursor commands reopen either. Rejected:
  plugin-side state keyed by file+line (breaks on edits, invisible in the
  doc).
- The review pane is polymorphic and agent-declared: task agents end with
  "DONE — review: <path>" naming their primary artifact; a document path
  opens in one stable right-side review pane, no path means code and goes
  to difit. Rejected: git-status heuristics for guessing the surface.
- difit is the code-review surface: on a task agent's DONE (or the
  "Review changes in difit" command), the plugin launches `difit .` over
  the vault's uncommitted changes; line comments flow back via difit's
  Copy All Prompt into the task's session tab. Prose review stays `@pi`
  markers in the doc — line-anchored code comments and text-anchored doc
  comments, one agent behind both.

## Build plan

- [x] Protocol: pin pi's RPC message shapes (stdin commands, stdout
      events, resume semantics) from pi-mono docs/source.
- [x] Scaffold: manifest, esbuild, TypeScript, `PiProcess` wrapper
      (spawn/kill/restart, JSON-lines framing, event dispatch).
- [x] Chat view: multi-instance `ItemView`; streaming markdown via
      `MarkdownRenderer`; input box with steering; render prior history on
      resume.
- [x] Session binding: resolve-or-create `pi-session` frontmatter; command
      "Open pi session for this note".
- [x] Command "Launch agent for task line": new session named for the task
      slug, seeded with task text + doc + resolved `[[links]]`; status
      written back to the line (running marker → checked box + links).
- [x] Command "Resolve @pi comments": send the resolution instruction to
      the note's bound session.
- [x] Process hygiene: each view destroys its pi process on close (loss-free
      — state lives in the session file); unload closes views. Open: no
      concurrency cap yet, and the gentler stdin-close shutdown needs an
      upstream patch (process handle is private).
- [x] difit integration: review-on-DONE (setting-gated), the review
      command, one managed difit process killed on unload.
- [ ] Drawer skills: task-doc + review-doc templates, comment-resolution
      skill, evals with mechanical checks (markers resolved and removed,
      checkbox structure, honest status).
- [ ] Live validation on a real vault (not possible in the build
      environment — no Obsidian, no pi).

## Risks

- RPC protocol stability: pi is young; the protocol may drift. Mitigation:
  pin the pi version in the plugin's requirements, keep `PiProcess` the
  only file that knows message shapes.
- Multi-process resource use: N open tabs = N pi processes. Mitigation:
  processes die with their tab; sessions make that loss-free.
- Frontmatter edits by agents and plugin can race Obsidian's own metadata
  cache. Mitigation: single writer per doc — the plugin writes frontmatter,
  agents write body.

## Done means

A vault where: opening a task doc and running "launch agent" on a task line
produces a new chat tab doing the work; closing and reopening that tab
resumes the same conversation; an `@pi` comment in the review doc gets
resolved by its bound session and the marker disappears; and quitting
Obsidian leaves no orphaned pi processes.
