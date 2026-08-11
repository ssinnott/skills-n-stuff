# plugins/

Standalone plugins that deserve their own install unit (instead of riding along
in the root `grab-bag` plugin) live here, one directory per plugin:

```
plugins/
  my-plugin/
    skills/                      # optional
    agents/                      # optional
    commands/                    # optional
    hooks/hooks.json             # optional
```

After adding one, register it in `.claude-plugin/marketplace.json` at the repo
root by appending to the `plugins` array. With `"strict": false`, the
marketplace entry is the plugin's entire definition and the plugin directory
needs no manifest of its own:

```json
{
  "name": "my-plugin",
  "source": "./plugins/my-plugin",
  "strict": false,
  "description": "..."
}
```

(Alternatively, give the plugin its own `.claude-plugin/plugin.json` and omit
`strict` — useful only if the plugin should also be installable outside this
marketplace.)
