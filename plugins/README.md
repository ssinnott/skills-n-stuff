# plugins/

Standalone plugins that deserve their own install unit (instead of riding along
in the root `grab-bag` plugin) live here, one directory per plugin:

```
plugins/
  my-plugin/
    .claude-plugin/plugin.json   # required: at least {"name": "my-plugin"}
    skills/                      # optional
    agents/                      # optional
    commands/                    # optional
    hooks/hooks.json             # optional
```

After adding one, register it in `.claude-plugin/marketplace.json` at the repo
root by appending to the `plugins` array:

```json
{ "name": "my-plugin", "source": "./plugins/my-plugin", "description": "..." }
```
