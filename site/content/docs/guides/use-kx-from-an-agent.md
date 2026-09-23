---
title: Use kx from an agent
description: kx mcp serves diagnostics, ownership, evidence and marks to an AI agent over MCP — read-only, and never touches your indexes.
weight: 9
---

`kx mcp` runs a Model Context Protocol server on stdin/stdout. An agent gets
the same view of the cluster kx gives you — health, ownership, events, logs,
usage, manifests, image scans — without ever touching the listing your
terminal's own indexes resolve against.

## Setup

For Claude Code:

```bash
claude mcp add kx -- kx mcp
```

For any client that reads its MCP server list as JSON:

```json
{
  "mcpServers": {
    "kx": {
      "command": "kx",
      "args": ["mcp"]
    }
  }
}
```

kx reads the same kubeconfig your shell does, so whatever
`kubectl config current-context` reports is where the agent starts.

## The tools

| Tool | Reads |
| --- | --- |
| `list_marks` | Every mark you've pinned, in any cluster. |
| `mark` | Pins a name to a resource — the one write the server makes. |
| `list_resources` | Names and namespaces of one kind. |
| `diagnose` | Health: one resource's findings, or a namespace sweep. |
| `tree` | Ownership: what a resource owns and is owned by. |
| `events` | Warning and normal events recorded against one resource. |
| `logs` | Recent container logs, one pod or a workload's pods aggregated. |
| `top` | Current CPU and memory, against limits or node capacity. |
| `get_yaml` | A resource's manifest, narrowable to a few fields, Secrets redacted. |
| `scan` | Image CVEs from the configured scanner, at most 50 images a call by default. |

Your MCP client lists each tool's arguments itself, from the server's
`tools/list` reply, with a description of every one.

## What it will never do

Every tool, `mark` included, is read-only against the cluster: kubectl only
ever runs `get`, `logs` and `top`; client-go only ever `get`s, `list`s and
`watch`es. Nothing an agent calls here deletes, patches, scales, execs,
port-forwards or edits anything.

`mark` is the one write, and it writes kx's own state, not the cluster — the
same `~/.kx/state.json` a mark set from your terminal lives in, so `@culprit`
resolves the same resource whether you or the agent set it. It only ever
adds: it refuses any name that is already a mark, even one on the same
resource, and there is no tool to remove one.

The server never touches the history your `kx get` builds. An agent's
`diagnose` or `tree` never saves a listing, so `kx 3` in your terminal
resolves the same resource before, during and after an agent session runs
alongside it.

Every kind, name and namespace an agent sends is validated before it reaches
kubectl's argv — a leading `-` is refused rather than read as a flag — so a
target can name a real resource but never inject one. Image references a
`scan` reads out of pod specs get the same treatment: one that starts with `-`
or names a scanner source such as `dir:` or `fs://` comes back as an error row
and never reaches the scanner.

{{% kx-note %}}
Marks are shared with your terminal, not private to the agent. `kx mark api 3`
at your prompt and an agent's `mark` tool write and read the same names, so a
mark you hand an agent, or one it sets and tells you about, works either way
from then on.
{{% /kx-note %}}

## Secrets

`get_yaml` on a Secret redacts every value under `data` and `stringData`, and
the `kubectl.kubernetes.io/last-applied-configuration` annotation, which
carries a Secret's whole last-applied manifest, plaintext data included. Keys
are kept; values become `<redacted>`.

Redaction goes by what kubectl returns as well as by what was asked for: any
manifest that comes back as a core `v1` Secret is redacted, however its kind
was spelled (`secret`, `secrets.v1.`) and whether it was named directly or
through a mark.

## Context follows your kubeconfig, live

The server keeps no state of its own about which cluster it is pointed at —
it reads your kubeconfig on every call. Switch context in another terminal
and the agent's next call reads the new cluster. Every result names the
`context` it came from, so an agent — and you, reading the transcript — can
tell which cluster answered.

## Scripting it by hand

Piping a request straight in closes stdin the moment `printf` finishes, and
the server reads that as the client hanging up: it cancels whatever was still
in flight, and the reply can be lost.

```bash
printf '...' | kx mcp             # the reply can be lost at EOF
(printf '...'; sleep 1) | kx mcp  # keep stdin open past the write
```

A real MCP client keeps its stdin pipe open for the life of the session, so
this only bites when driving the server by hand.
