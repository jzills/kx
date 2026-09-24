---
title: Use kx from an agent
description: kx mcp serves diagnostics, ownership, evidence and marks to an AI agent over MCP — read-only against the cluster; it reads your indexes always, and saves its own listings only if you opt in.
weight: 9
---

`kx mcp` runs a Model Context Protocol server on stdin/stdout. An agent gets
the same view of the cluster kx gives you — health, ownership, events, logs,
usage, manifests, image scans — and can read the numbers from your terminal's
own listing, without ever writing to it unless you ask it to.

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

By default, the server never touches the history your `kx get` builds. An
agent's `list_resources`, `diagnose`, `tree` or `top` never saves a listing, so
`kx 3` in your terminal resolves the same resource before, during and after
an agent session runs alongside it. That only changes if you start the server
with `--write-listings` — see the next section.

Every kind, name and namespace an agent sends is validated before it reaches
kubectl's argv — a leading `-` is refused rather than read as a flag — so a
target can name a real resource but never inject one. Image references a
`scan` reads out of pod specs get the same treatment: one that starts with `-`
or contains `://` (a source URL to scout, such as `fs:///home`) — or, under
grype, starts with one of its source prefixes such as `dir:` — comes back as
an error row and never reaches the scanner.

{{% kx-note %}}
Marks are shared with your terminal, not private to the agent. `kx mark api 3`
at your prompt and an agent's `mark` tool write and read the same names, so a
mark you hand an agent, or one it sets and tells you about, works either way
from then on.
{{% /kx-note %}}

## Sharing indexes with the agent

Indexes cross the bridge between your terminal and the agent in both
directions.

The server uses the `KX_STATE` and `KUBECONFIG` its MCP client launched it
with, not your shell's. If your terminal sets a different `KX_STATE`, the
bridge looks at a different state file from yours, in both directions.

**Terminal → agent.** You run `kx get pods`, then tell an agent "diagnose 3".
Give the agent an `index` of `3` in place of a `kind`/`name` target — a mark
also works the same way — and it resolves against whatever listing your
terminal currently has open, the same row `kx describe 3` would name. This
read is always on, needs no setup, and never writes anything: the agent
learns the resolved name and namespace back in the result, never the number
it sent.

**Agent → terminal.** By default an agent's own listings — `list_resources`,
a `diagnose` sweep, `tree`, `top` — are never saved, so there's nothing in
them for you to spend from your shell. Start the server with
`--write-listings` and that changes: those four tools save what they list to
your kx history exactly as the matching CLI command would, and return each
row's index. An agent's `tree` call with a target (the analogue of
`kx tree 3`) still saves the whole walk it returns, so a node it shows you
keeps the index it was saved at. Each listing is stamped with the context it
was read in, so if you switch context while an agent's sweep is running, its
numbers are refused in the new context rather than resolved there.

```bash
claude mcp add kx -- kx mcp --write-listings
```

An agent-made listing is tagged, so you can tell it apart from your own:
`kx state` shows it with a `via kx mcp` caption, `kx state --all` adds a VIA
column naming `kx mcp` beside each agent entry, and `kx delete`/`kx drain`
add "— from a kx mcp listing" to their confirm prompt when the index you're
spending resolves against one. Past that tag, it's an ordinary entry on your
history stack — `kx state back` steps behind it like any other listing. That
cuts both ways: agent listings count toward `max_history` (10 by default), so
a busy agent can push your own listings off the stack, and an agent save while
you've stepped back with `kx state back` drops the entries ahead of you, the
same as any new listing would.

**Accepted risk.** With `--write-listings` on, an agent's listing becomes
your *current* listing the moment it saves, so `kx delete 3` right after can
mean the agent's row 3, not the one you last ran `kx get` for. Every
mutating command now says so when the index it resolves came from an agent's
listing. `kx delete` and `kx drain` fold that into their confirm prompt, so
you see it before you answer; skip the prompt with `--yes`/`-y` and they print
the stderr notice below in its place. `kx scale`, `kx rollout` (`restart`,
`undo`, `pause` and `resume`), `kx cordon`, `kx uncordon`, `kx debug`,
`kx edit`, `kx exec`, `kx label`, `kx annotate` and `kx cp` print a muted line
to stderr before they act: `Index 3 is from a kx mcp listing —
Pod/web-healthy-abc123. Run 'kx state' to see it.` It is a notice, not a
prompt — nothing pauses, stdout is unchanged, and the exit code doesn't
change either. Commands that only read — `kx describe`, `kx logs`, `kx yaml`,
`kx port-forward`, `kx events`, `kx tree`, `kx diag`, and `kx rollout status`
and `history` — print nothing. `kx state` shows which listing is current; `kx state back` is
the way out if it isn't the one you meant. The same live-resolution rule that
makes index reads useful also makes them relative: an index always resolves
against whatever listing is current *at the moment of the call*, so relisting
between speaking a number and the agent acting on it changes what that number
means — in the terminal or from an agent, alike.

**Concurrent writers.** The CLI and a running `kx mcp` server can both be
mid-write to `~/.kx/state.json` — a mark from your terminal while
`--write-listings` saves a sweep, say. Every write takes a lock file beside
the state file first, so one write finishes before the next one reads,
instead of the two racing and one silently overwriting the other. A writer
that can't get the lock within a few seconds, because another kx holds it,
fails rather than hanging. Where no lock can be taken at all — a filesystem
that doesn't support locking, or a lock file kx can't open even read-only —
the write goes ahead unlocked, as kx wrote before the lock existed, rather
than refusing every write.

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
