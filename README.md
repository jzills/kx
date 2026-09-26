<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/banner.svg" alt="kx — kubectl, indexed." width="800"/>
</div>

<div align="center">

# kubectl, indexed

</div>

<div align="center">

[![PyPI version](https://img.shields.io/pypi/v/kx-cli?style=flat-square&color=3fb950&labelColor=21262d)](https://pypi.org/project/kx-cli/)
[![License](https://img.shields.io/github/license/jzills/kx?style=flat-square&color=3fb950&labelColor=21262d)](https://github.com/jzills/kx/blob/main/LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/jzills/kx/pr.yml?style=flat-square&color=3fb950&labelColor=21262d&label=CI)](https://github.com/jzills/kx/actions/workflows/pr.yml)

[Documentation](https://jzills.github.io/kx/) · [Install](https://jzills.github.io/kx/docs/getting-started/install/) · [Guides](https://jzills.github.io/kx/docs/guides/) · [Commands](https://jzills.github.io/kx/docs/reference/commands/)

</div>

Stop copying resource names out of `kubectl get`. `kx get` numbers every row,
and every command after it takes the number — `kx logs 3`, `kx diag 3`,
`kx delete 3..5`.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/demo.gif" alt="kx demo" width="800"/>
</p>

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/)**

</div>

## Install

Requires `kubectl` on your PATH. Every install path delivers the same prebuilt
binary — no Python runtime, no dependencies.

With [uv](https://docs.astral.sh/uv/) (recommended), [pipx](https://pipx.pypa.io/), or pip:

```bash
uv tool install kx-cli
pipx install kx-cli
pip install kx-cli
```

As a kubectl plugin via [krew](https://krew.sigs.k8s.io/), where kx is published as `idx`:

```bash
kubectl krew install idx
alias kx="kubectl idx"
```

Or run it without installing — the package is `kx-cli`, the command is `kx`:

```bash
uvx --from kx-cli kx get pods
pipx run --spec kx-cli kx get pods
```

Standalone binaries for linux, macOS and Windows are attached to every
[GitHub Release](https://github.com/jzills/kx/releases).

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/getting-started/install/)**

</div>

## Usage

A `kx` session at a glance — one listing, then everything after it by number.

```bash
kx get pods    # lists pods, numbering each row
kx logs 3      # the third pod
kx diag 3      # why it's unhealthy
```

Indexes come several at a time, as ranges, or narrowed.

```bash
kx delete 3 5                   # several at once
kx delete 3..7                  # an inclusive range, walking either direction, trimmed to the listing
kx delete ..5                   # open at the start
kx delete 5..                   # open to the end of the listing
kx get pods -m api              # --match/-m filters rows by name substring
kx get pods -n prod -l app=api  # anything else passes through to kubectl
```

- kubectl's own flags pass through — `kx delete 3 --force --grace-period=0`,
  `kx logs 3 -f --tail=100`. `-n` beside an index is refused: the index already
  carries the namespace it was listed from.
- `-A` listings are indexed too, each row with its own namespace.
- Known kinds drop the `get` — `kx pods`, `kx deploy -n kube-system` —
  kubectl's shorthands and your CRDs included.
- `--watch`/`-w` redraws the table in place rather than appending lines.
- `kx completion <shell>` completes indexes with the resource behind them:
  `kx describe <TAB>` offers `1  api-7d8f (Pod)`, not a bare number.

## Triage a namespace

Bare `kx diag` sweeps the current namespace — every workload kind, plus
Services, PVCs, Ingresses and pods nothing owns — and ranks what's unhealthy.
It reads live usage too, so a pod running hot against its memory limit is
flagged as an OOMKill risk before it dies.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/diag.gif" alt="kx diag demo" width="800"/>
</p>

`kx diag <index>` diagnoses a single resource: a top level verdict, a findings
summary, a per-pod status table, log tails from broken containers and warning
events — one screen instead of four kubectl commands.

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/triage-a-namespace/)**

</div>

## Read a Secret in plaintext

`kx secret <index> --decode` prints an indexed Secret's keys and values decoded.
`--key`/`-k` prints a single value raw — no banner, no wrapping — so it drops
straight into a shell.

```bash
export PGPASSWORD=$(kx secret 1 --decode -k password)
```

Bare `kx secret --decode` decodes every Secret in the namespace in one call,
confirming first — that prints every credential you have.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/secret.gif" alt="kx secret --decode demo" width="800"/>
</p>

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/read-a-secret/)**

</div>

## Scan images for CVEs

`kx scan <index>` scans the unique container images of an indexed workload.
Bare `kx scan` sweeps every workload in the namespace. Results come back as a
severity summary, or the full per-image CVE report with `--full`.

Requires the CLI for the selected engine — [Docker Scout](https://docs.docker.com/scout/)
by default, or [Trivy](https://trivy.dev/) and
[Grype](https://github.com/anchore/grype) via `kx engine`.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/scan.gif" alt="kx scan demo" width="800"/>
</p>

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/scan-images/)**

</div>

## See what owns what

`kx tree <index>` walks the ownership graph — Deployment to ReplicaSet to Pods —
and indexes every node it draws, so anything in the tree is one number away.
Bare `kx tree` graphs the whole namespace.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/tree-html.png" alt="kx tree dashboard" width="800"/>
</p>

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/ownership-tree/)**

</div>

## Reports in the browser

`--html` on `kx diag`, `kx scan`, `kx tree`, and `kx top` renders the same
analysis as a page and opens it in your browser. It binds `127.0.0.1` only and
writes nothing to disk.

The page is drawn in your active theme. Sweep rows expand into that resource's
full report, image rows into the CVEs behind their counts — detail the terminal
has no room for.

`--out <path>` writes the page to a file instead of serving it, which is what
you want in CI — `kx diag --out report.html` is the whole command.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/diag-html.png" alt="kx diag --html dashboard" width="800"/>
</p>

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/browser-reports/)**

</div>

## Spend an index anywhere

`kx` wraps two dozen of kubectl's verbs. `kx ref` covers the rest, and every
tool that isn't kubectl: it prints what an index refers to, as an argument
fragment that drops straight into another command.

```bash
kx ref 3                                     # pod/web-abc-xyz -n prod
kubectl exec $(kx ref 3) -- sh               # a verb kx doesn't wrap
kubectl get $(kx ref 1..3)                   # one line each, so ranges work too
stern $(kx ref 3 --name) -n $(kx ref 3 --namespace)
kx ref 1..9 --name | xargs -n1 some-tool
```

`--name`, `--namespace` and `--kind` print one field alone, and nothing here
touches the cluster, so `kx ref` answers instantly even with nothing reachable.

## Marks

An index is a position, and positions move — the next `kx get` renumbers
everything out from under it. A mark is a name you choose instead, pinned to
one resource, so it keeps working across every listing that comes after it.

```bash
kx mark api 3          # pin what index 3 is right now
kx logs @api -f        # spend it where a command takes an index
kx exec @api -- sh
kx mark                # list marks
kx unmark api
kx unmark --all
```

A mark survives re-listing, which is what an index cannot do. It is pinned to
the cluster it was taken in, and will not resolve in another — the same name
means a different resource there, or none at all. `kx state drop --all`
leaves marks alone; `kx unmark --all` is what removes them.

`kx ns` and `kx context` take an index but not a mark, because a slot is not
a resource; `kx cp` parses its own `index:path` and takes one too.

## Use kx in CI

`--fail-on <severity>` turns a sweep into a build gate, and `--json` prints the
same analysis as a document for anything downstream.

```bash
kx diag -A --fail-on critical                        # 0 if the cluster is healthy, 2 if not
kx scan -A --fail-on high                            # the same, for image vulnerabilities
kx scan -n prod --fail-on high --json | jq '.images[] | select(.counts.critical > 0)'
kx diag -A --fail-on critical --out report.html      # publishes the report *and* fails the build
kx diag -A --fail-on warning --since 24h             # ignore what failed before today
```

Exit **2** means findings breached the threshold, **1** means kx itself failed —
so a pipeline can tell "the cluster is sick" from "the check never ran".

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/use-kx-in-ci/)**

</div>

## MCP server

`kx mcp` runs a Model Context Protocol server on stdio, so an agent — Claude
Code, an IDE, an agent framework — reads a cluster the way you do: by kind and
name, by a mark, or by an index from your current listing.

```bash
claude mcp add kx -- kx mcp
```

Its ten tools are read-only against the cluster; `mark`, the one write, only
adds a name to kx's own state. Start it with `--write-listings` and the
agent's listings join your history, tagged as the agent's, so you can spend its
numbers yourself — and kx warns before a mutating command does.

```bash
claude mcp add kx -- kx mcp --write-listings
```

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/guides/use-kx-from-an-agent/)**

</div>

## State and history

`kx` keeps up to 10 `kx get` results in `~/.kx/state.json`, with a cursor
marking the entry indexes resolve against.

```bash
kx state              # the listing indexes currently resolve against
kx state --all        # the whole history, with positions
kx state 2            # jump to position 2
kx state back         # step back one (forward steps the other way)
kx state drop 2       # remove position 2 (--all clears everything, slots included, marks untouched)
kx state drop --empty # drop the entries whose listing found nothing
```

Each entry remembers the context it was listed in, so a staging index is never
resolved against production — `kx` refuses and relists instead.

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/concepts/state/)**

</div>

## Configuration

`kx` reads `~/.kx/config.toml`, and every setting takes a `KX_*` environment
override. The two worth changing have commands of their own — `kx theme` and
`kx engine` both persist your choice.

Styling is dropped when stdout isn't a terminal, so `kx get pods | grep worker`
stays clean. [`NO_COLOR`](https://no-color.org/) is honored too.

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/concepts/configuration/)**

</div>

## Themes

`kx theme` lists the available themes with a preview of each. `kx theme <name>`
persists your choice, by name or index.

<p align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/theme.gif" alt="kx theme demo" width="800"/>
</p>

Eleven prefabs ship with it: `github-dark` (default), `dracula`, `nord`,
`gruvbox`, `solarized-dark`, `catppuccin-mocha`, `tokyo-night`, `rose-pine`,
`mono`, `light` and `plain`.

<div align="center">

**[Full documentation →](https://jzills.github.io/kx/docs/concepts/themes/)**

</div>

## Commands

Every command takes indexes from the listing `kx get` last produced. The
[command reference](https://jzills.github.io/kx/docs/reference/commands/)
covers every argument and flag.

<details>
<summary><b>All commands</b></summary>

<!-- commands-table-start -->
| Command | Description |
|---|---|
| `kx annotate <index> [<key=value>...]` | Set or remove annotations on an indexed resource. |
| `kx annotations <index>...` | Show annotations for one or more indexed resources. |
| `kx context [<index>]` | List kubeconfig contexts, or switch to an indexed one; alias: kx contexts. |
| `kx cordon <index>...` | Mark one or more indexed Nodes unschedulable. |
| `kx cp <src> <dest>` | Copy files to or from an indexed pod via kubectl cp. |
| `kx debug <index> [<command>...]` | Open a debug shell on an indexed Pod (an ephemeral container, for images with no shell) or Node (a privileged pod on the host). |
| `kx delete <index>...` | Delete one or more indexed resources (prompts for confirmation unless --yes). |
| `kx describe <index>...` | Show full kubectl describe output for one or more indexed resources. |
| `kx diagnostic [<index>]` | Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, Ingress, Pod, or Node, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag. |
| `kx drain <index>` | Evict the pods from an indexed Node (prompts for confirmation unless --yes). |
| `kx edit <index>` | Open an indexed resource in your editor via kubectl edit. |
| `kx events <index>...` | Show Kubernetes events for one or more indexed resources. |
| `kx exec <index> [<command>...]` | Open an interactive shell in an indexed Pod, Deployment, ReplicaSet, StatefulSet or DaemonSet (bash, falling back to sh). |
| `kx get <resource> [<index>...]` | List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3). |
| `kx label <index> [<key=value>...]` | Set or remove labels on an indexed resource. |
| `kx labels <index>...` | Show labels for one or more indexed resources; --selector formats output as a label selector. |
| `kx logs <index>...` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, Jobs, and Services. |
| `kx mark [<name>] [<index>]` | Pin an indexed resource to a name that survives re-listing; with no arguments, lists marks. |
| `kx namespace [<index>]` | List namespaces, or switch to an indexed one; alias: kx ns. |
| `kx port-forward <index> <port>` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx ref <index>...` | Print what an index refers to, for commands kx doesn't wrap. |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx scan [<index>]` | Scan the unique container images of an indexed workload for vulnerabilities, or a whole namespace when no index is given (-n to pick one, -A for every namespace); prints a severity summary table by default, or the raw scanner output with --full. Requires the CLI for the selected scan engine (Docker Scout by default; Trivy or Grype via --engine — see kx engine). |
| `kx secret [<index>...]` | List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets. |
| `kx top [<resource>]` | List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits. |
| `kx tree [<index>]` | Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace. |
| `kx uncordon <index>...` | Mark one or more indexed Nodes schedulable again. |
| `kx unmark [<name>...]` | Remove marks by name; --all removes every mark. |
| `kx yaml <index>...` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx state [<position>]` | Show current state, jump to a history position, list all entries with --all, or expand the switch targets with --targets. |
| `kx engine [<name>]` | List available scan engines or persist a default choice by name or index. |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
| `kx mcp` | Serve kx's diagnostics, ownership trees, evidence and marks to AI agents over MCP (stdio). |
| `kx completion` | Generate a shell completion script for kx (bash, zsh, fish, powershell). |
<!-- commands-table-end -->

</details>

## Contributing

Building, testing and releasing kx are covered in
[CONTRIBUTING.md](https://github.com/jzills/kx/blob/main/CONTRIBUTING.md).
