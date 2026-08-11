# skills-n-stuff

A junk drawer of skills, agents, and commands, packaged so it works as a
[Claude Code plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces)
and as a [pi](https://github.com/badlogic/pi-mono) package — from the same files.

## Layout

```
.claude-plugin/
  marketplace.json    # marks this repo as a Claude Code marketplace
  plugin.json         # the repo root is itself a plugin: "grab-bag"
skills/               # one dir per skill, each with a SKILL.md (agentskills format)
agents/               # Claude Code subagents (markdown + YAML frontmatter)
commands/             # Claude Code slash commands / pi prompt templates
extensions/           # pi extensions (TypeScript, pi-only)
plugins/              # standalone plugins that deserve their own install unit
package.json          # "pi" key maps skills/, commands/, and extensions/ for pi
```

The top-level `skills/`, `agents/`, and `commands/` directories are the junk
drawer — drop things in and they ship with the `grab-bag` plugin. Anything big
enough to want its own install unit goes under `plugins/` (see
`plugins/README.md`) and gets its own entry in `marketplace.json`.

## Using with Claude Code

```
/plugin marketplace add ssinnott/skills-n-stuff
/plugin install grab-bag@skills-n-stuff
```

That loads every skill in `skills/`, every agent in `agents/`, and every
command in `commands/`. Update later with `/plugin marketplace update
skills-n-stuff`.

## Using with pi

```
pi install https://github.com/ssinnott/skills-n-stuff
```

Pi reads the `pi` key in `package.json`: skills come from `skills/`, the files
in `commands/` are registered as prompt templates, and extensions load from
`extensions/`. Update with `pi update`, remove with `pi remove`.

Notes on cross-harness behavior:

- **Skills** are plain agentskills-format directories (`SKILL.md` with `name`
  and `description` frontmatter), which both harnesses consume natively.
- **Commands / prompt templates** share a format closely enough (markdown body,
  `description` and `argument-hint` frontmatter, `$ARGUMENTS`/`$1` variables)
  that one file serves both. Stick to those fields for anything you want
  working in both harnesses.
- **Agents** are Claude Code-only; pi has no subagent concept and ignores the
  `agents/` directory.
- **Extensions** are pi-only; Claude Code ignores the `extensions/` directory.

## Adding stuff

- **Skill**: `skills/<name>/SKILL.md` with `name` + `description` frontmatter.
  The description should read as trigger guidance ("Use when ...").
- **Agent**: `agents/<name>.md` with `name` + `description` frontmatter and the
  system prompt as the body.
- **Command**: `commands/<name>.md` with a `description` frontmatter line.
- **Extension** (pi-only): `extensions/<name>.ts` default-exporting a factory
  that receives `ExtensionAPI` — it can subscribe to events (`pi.on`), register
  tools (`pi.registerTool`) and slash commands (`pi.registerCommand`). Pi
  compiles the TypeScript itself; npm dependencies are allowed if you add a
  `package.json`/lockfile next to the extension.
- **Plugin**: its own directory under `plugins/` plus an entry in
  `.claude-plugin/marketplace.json` — see `plugins/README.md`.
