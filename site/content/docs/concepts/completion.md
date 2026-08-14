---
title: Shell completion
description: Tab-completion that names the resource behind each index, without calling the cluster.
weight: 5
---

```bash
kx completion <bash|zsh|fish|powershell>
```

prints a completion script for that shell. What makes it worth installing is
what it completes: not just command names and flags, but the indexes from your
saved listing, each labelled with the resource it points at.

```
$ kx describe <TAB>
1  api-7d8f-p4k2 (Pod)
2  billing-5c4b-x9m1 (Pod)
3  cache-9a1e-t7w3 (Pod)
```

Which is the difference between remembering a number and reading one.

## What completes

| Position | Candidates |
| --- | --- |
| The first word | Commands, plus every kind the shorthand accepts |
| `<index>` | Indexes from the current listing, with names and kinds |
| `<index>` for `kx ns` / `kx context` | The namespace and context slots, not the history stack |
| `<position>` for `kx state` | History positions |
| `<action>` for `kx rollout` | `status`, `restart`, `pause`, `resume`, `history`, `undo` |
| `<name>` for `kx theme` / `kx engine` | The palettes and scan engines |
| `-n` | Namespace names |
| `<src>` / `<dest>` for `kx cp` | Local paths, from the shell's own file completion |

All of it is answered from `~/.kx/state.json` and registries compiled into the
binary. Nothing shells out to kubectl or calls the API server, because a
completion that waits on a cluster is a completion people turn off.

## Installing it

### zsh

```bash
kx completion zsh > "${fpath[1]}/_kx"
```

Then start a new shell. If completion isn't initialised yet, add
`autoload -Uz compinit && compinit` to `~/.zshrc` first.

### bash

```bash
source <(kx completion bash)
```

To make it permanent, write it somewhere `bash-completion` reads:

```bash
kx completion bash > /etc/bash_completion.d/kx          # system-wide
kx completion bash > ~/.local/share/bash-completion/completions/kx
```

### fish

```bash
kx completion fish > ~/.config/fish/completions/kx.fish
```

### PowerShell

```powershell
kx completion powershell | Out-String | Invoke-Expression
```

Append that to your profile to keep it.

{{% kx-note %}}
Installed as a krew plugin, the command is `kubectl idx`. Completion scripts
are generated for a command called `kx`, so they work against the
`alias kx="kubectl idx"` from the [install page](../../getting-started/install/)
rather than against `kubectl idx` typed out.
{{% /kx-note %}}
