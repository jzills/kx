---
title: Configuration
description: ~/.kx/config.toml, the KX_* overrides, and when output is styled.
weight: 3
---

`kx` reads `~/.kx/config.toml`. Every key has an environment variable that
overrides it, so a shell can differ from the file without editing anything.

```toml
theme = "dracula"
engine = "trivy"
max_history = 25
shells = ["zsh", "bash", "sh"]
debug_image = "alpine"
diag_max_age = "7d"
```

`kx --version` prints the path it resolved, which is the quickest way to find
out whether the file you are editing is the file it reads.

## Keys

| Key | Environment | Default | What it does |
| --- | --- | --- | --- |
| `theme` | `KX_THEME` | `github-dark` | Colour palette for all output; see [themes](../themes/). |
| `engine` | `KX_ENGINE` | `scout` | Default scanner for `kx scan` — `scout`, `trivy` or `grype`. |
| `max_history` | `KX_MAX_HISTORY` | `10` | How many `kx get` results the [history stack](../state/) keeps. |
| `shells` | `KX_SHELLS` | `bash, sh` | Shell candidates `kx exec` tries, in order. |
| `debug_image` | `KX_DEBUG_IMAGE` | `busybox` | Image `kx debug` attaches to a pod; `--image` overrides it per run. |
| `diag_max_age` | `KX_DIAG_MAX_AGE` | unset | How far back [`kx diag`](../../reference/commands/diagnostic/) looks for evidence; unset reports everything, `--since` overrides it per run. |
| `theme_disable` | `KX_THEME_DISABLE` | `false` | Turn off styling entirely — the same as `--no-color`. |

`shells` is a list in the file and a comma-separated string in the
environment: `KX_SHELLS=zsh,bash,sh`.

`diag_max_age` is a duration — `30m`, `12h`, `7d` — and `0` means no limit,
which is also what you get by leaving it out. Setting it is worth it because
evidence drives a finding, a finding drives the verdict, and a verdict drives
`--fail-on`: unbounded, one `FailedScheduling` from three weeks ago holds a
resource at `warnings`, and a CI gate red, forever.

Two commands write to the file rather than making you edit it:

```bash
kx theme dracula     # persists theme
kx engine trivy      # persists engine
```

## When output is styled

Styled output is emitted only when stdout is a terminal. Piped or redirected
output is plain text, so this stays clean:

```bash
kx get pods | grep worker
kx get pods > pods.txt
```

The [`NO_COLOR`](https://no-color.org/) convention is honoured too. It is a
terminal-wide convention rather than a `kx` setting, which is why it isn't in
the table above — `theme_disable` and `--no-color` are the `kx` ways to say
the same thing.

## The files kx owns

| Path | Environment | Contents |
| --- | --- | --- |
| `~/.kx/config.toml` | `KX_CONFIG` | The settings above. |
| `~/.kx/state.json` | `KX_STATE` | Saved listings and the cursor; see [state](../state/). |

`KX_CONFIG` and `KX_STATE` point kx at a different file entirely, for a
terminal or CI job that wants its own config or history rather than sharing
the one in `~/.kx` — unlike the settings table above, neither can be set
from inside the file it names.

Both paths are printed by `kx --version` and on the root `kx --help` screen.
Neither is required to exist — `kx` runs on defaults without them.
