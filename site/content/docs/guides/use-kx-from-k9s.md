---
title: Use kx from k9s
description: Five k9s plugins that run kx diag, tree, scan, events and mark on the row under the cursor, and wait for Enter before handing the screen back.
weight: 10
---

k9s numbers nothing — you move a cursor — so kx's indexes have no place inside it.
What does carry over is everything kx does with a resource once it has one, because every kx command also takes a resource as kind/name.
The kx plugin pack binds five keys that hand the selected row to kx in that form, in the row's namespace and k9s's context.

## Install

The pack is one file, for the plugins directory k9s reads:

```bash
curl -fsSL https://jzills.github.io/kx/k9s/kx.yaml \
  -o "${XDG_CONFIG_HOME:-$HOME/.config}/k9s/plugins/kx.yaml"
```

On macOS without `XDG_CONFIG_HOME` set, k9s reads `~/Library/Application Support/k9s` instead.
Running `k9s info` prints the directory either way.
The pack needs kx 0.12 or later, the first release where these commands take kind/name and `--context`.

## The keys

| Key | Runs | Views |
|---|---|---|
| Shift-D | `kx diag` | workloads, Pods, Services, PVCs, Ingresses, Nodes |
| Shift-T | `kx tree` | workloads, Pods, Services, Namespaces |
| Shift-V | `kx scan` | Pods and workloads |
| Shift-E | `kx events` | every view |
| Shift-K | `kx mark` | every view |

None of them is a key k9s binds itself in those views.
Each runs in the foreground, then waits for Enter so the report stays on screen until you are done reading it.

## From k9s back to the terminal

The one key that outlives the k9s session is Shift-K.
It asks for a name and pins the row as a [mark](../../concepts/marks/), so the same resource is one word away in a plain terminal:

```bash
kx logs @api -f
kx rollout restart @api
```

The mark is recorded against k9s's context, so it is spent there and refused from any other, as every mark is.
A `kx tree` run from k9s is numbered and saved as usual when k9s's context is your current one, and printed unnumbered when it is another.
