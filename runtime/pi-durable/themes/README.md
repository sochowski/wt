# Native palette data

`dark.json` and `light.json` are the published Pi coding-agent 0.87.1 palettes
(MIT, Mario Zechner; https://github.com/earendil-works/pi). These are data only,
not private SDK imports or a native engine. User themes are read from the actual
agent directory. No theme/settings/auth file is written and no watcher is started.

The bounded renderer uses the native semantic color format and public pi-tui
components. It is NOT the native InteractiveMode or full extension host.
