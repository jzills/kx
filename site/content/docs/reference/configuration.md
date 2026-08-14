---
title: Configuration keys
description: Every key in ~/.kx/config.toml and the environment variable that overrides it.
weight: 2
---

The reference table. [Configuration](../../concepts/configuration/) explains
what the settings do and when styling applies.

| Key | Environment | Type | Default |
| --- | --- | --- | --- |
| `theme` | `KX_THEME` | string | `github-dark` |
| `engine` | `KX_ENGINE` | string | `scout` |
| `max_history` | `KX_MAX_HISTORY` | integer | `10` |
| `shells` | `KX_SHELLS` | list of strings | `["bash", "sh"]` |
| `debug_image` | `KX_DEBUG_IMAGE` | string | `busybox` |
| `theme_disable` | `KX_THEME_DISABLE` | boolean | `false` |

Environment variables win over the file. `shells` is a TOML array in the file
and a comma-separated string in the environment:

```bash
KX_SHELLS=zsh,bash,sh kx exec 1
```

A complete file:

```toml
theme = "dracula"
engine = "trivy"
max_history = 25
shells = ["zsh", "bash", "sh"]
debug_image = "alpine"
theme_disable = false
```

Nothing here is required — kx runs on the defaults with no config file at all.

## Paths

| Path | Contents |
| --- | --- |
| `~/.kx/config.toml` | The keys above. |
| `~/.kx/state.json` | Saved listings and the history cursor; see [state](../../concepts/state/). |

`kx --version` prints both, resolved, along with the version and build.

## Also honoured

| | |
| --- | --- |
| `NO_COLOR` | The [cross-tool convention](https://no-color.org/). Any non-empty value disables styling. |
| `--no-color` | Per-run, on any command. |
| stdout not a terminal | Styling is dropped automatically, so pipes and redirects stay plain. |
