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
| `diag_max_age` | `KX_DIAG_MAX_AGE` | duration string | `"24h"` |
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
diag_max_age = "7d"
theme_disable = false
```

`diag_max_age` is a duration: `30m`, `12h`, `7d`. `"0"` removes the limit. It
bounds how far back [`kx diag`](../commands/diagnostic/) looks for evidence.

Nothing here is required — kx runs on the defaults with no config file at all.

## Paths

| Path | Environment | Contents |
| --- | --- | --- |
| `~/.kx/config.toml` | `KX_CONFIG` | The keys above. |
| `~/.kx/state.json` | `KX_STATE` | Saved listings and the history cursor; see [state](../../concepts/state/). |

`KX_CONFIG` and `KX_STATE` point kx at a different file entirely, rather than
overriding one key the way the settings table above does — useful for a
terminal or CI job that wants its own config or history instead of sharing
the one in `~/.kx`. Neither can be set from inside the file it names, so
there's no `config.toml` equivalent for them.

`kx --version` prints both paths, resolved, along with the version and build.

## Also honoured

| | |
| --- | --- |
| `NO_COLOR` | The [cross-tool convention](https://no-color.org/). Any non-empty value disables styling. |
| `--no-color` | Per-run, on any command. |
| stdout not a terminal | Styling is dropped automatically, so pipes and redirects stay plain. |
