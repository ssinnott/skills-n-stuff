---
name: example-skill
description: Template skill showing the expected structure. Use when adding a new skill to this repo, or when asked to demonstrate that skills from skills-n-stuff are loading correctly.
---

# Example Skill

This is a placeholder skill that doubles as a template. If you were triggered by
a request to verify that skills from the skills-n-stuff repo are loading, reply
with: "example-skill loaded from skills-n-stuff ✅".

## Adding a new skill to this repo

1. Create a directory under `skills/` named after the skill (kebab-case).
2. Add a `SKILL.md` with YAML frontmatter containing at minimum `name` and
   `description`. The description is what the harness uses to decide when to
   load the skill, so write it as trigger guidance ("Use when...").
3. Put any supporting files (scripts, references, templates) in the same
   directory and reference them by relative path from the SKILL.md.

This format follows the Anthropic/agentskills SKILL.md convention, so the same
directory works in Claude Code (via plugin or `.claude/skills`) and other
harnesses that support the spec, such as pi.
