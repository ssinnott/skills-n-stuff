---
name: pi-tasks-setup
description: Verify, fix, and configure everything the pi-tasks Obsidian plugin needs — pi with a provider, gh auth, difit via npx, a git-backed vault, the plugin built and installed, settings pointing at off-PATH binaries. Use when asked to set up, install, configure, diagnose, or troubleshoot pi-tasks or its prerequisites ("pi-tasks isn't working", "set up my vault for pi agents").
---

# pi-tasks setup

Setting up pi-tasks means proving each link in a chain works, not
assuming it — a silently broken link surfaces later as a cryptic notice
in Obsidian. Verify with real commands now and keep the output as
evidence.

Check, in order (ask for the vault path if you don't know it):

- **pi**: `pi --version`, then confirm a provider is configured — a
  models list or a one-line test prompt; a bare install with no API key
  passes `--version` and fails every session.
- **node/npx**: `npx --version`; **difit**: `npx difit --version`
  (first run downloads it — that's normal, not a failure).
- **gh**: `gh auth status`. Only PR/issue tracking needs it — a failure
  here is a note, not a blocker.
- **vault is a git repo**: `git -C <vault> rev-parse --git-dir`. Diff
  review needs git; document review works without it.
- **plugin installed**: `manifest.json`, `main.js`, `styles.css` present
  in `<vault>/.obsidian/plugins/pi-tasks/`. If missing, build from the
  plugin folder (`npm install && npm run build`) and copy them in.
  Enabling under Community plugins can't be verified from a shell — tell
  the user to check it, don't claim it.
- **profiles**, when the plugin's `data.json` names any: each mapped
  directory exists and holds a `settings.json`; an empty profile dir
  runs pi with no packages at all. Fix by
  `PI_CODING_AGENT_DIR=<dir> pi install <package>` per profile.

Fix what failed, verify again, and only then move on: install steps for
missing tools, `git init` for the vault (with consent — it's their
vault), build-and-copy for the plugin. If a binary lives off PATH, write
the plugin settings to `<vault>/.obsidian/plugins/pi-tasks/data.json`
(`piBinaryPath`, `difitCommand`, `ghPath`).

Offer, don't impose: `pi install` for a skills repo so agents share
skills, and a starter task document wired for the 2-minute first-use
flow from the plugin README.

Report a checklist — one line per prerequisite, pass or fail, with the
command and what it actually said. Never mark a line passed without its
command's output, and never call setup complete while a line is failed:
say what's broken and give the exact fix.
