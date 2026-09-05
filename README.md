<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/banner.svg" alt="kx — kubectl, indexed." width="800"/>
</div>

<br>

<div align="center">

# kubectl, indexed

</div>

<div align="center">

[![PyPI version](https://img.shields.io/pypi/v/kx-cli?style=flat-square&color=3fb950&labelColor=21262d)](https://pypi.org/project/kx-cli/)
[![License](https://img.shields.io/github/license/jzills/kx?style=flat-square&color=3fb950&labelColor=21262d)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/jzills/kx/pr.yml?style=flat-square&color=3fb950&labelColor=21262d&label=CI)](https://github.com/jzills/kx/actions/workflows/pr.yml)

</div>

`kx` is a kubectl wrapper that adds index-based resource selection. Run
`kx get <resource>` once, then reference any result by number instead of typing
full resource names.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/demo.gif" alt="kx demo" width="800"/>
</div>

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

[Full guide →](https://jzills.github.io/kx/docs/getting-started/install/)

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
kx delete 3..7                  # an inclusive range, walking either direction
kx delete ..5                   # open at the start
kx delete 5..                   # open to the end of the listing
kx get pods -m api              # --match/-m filters rows by name substring
kx get pods -n prod -l app=api  # anything else passes through to kubectl
```

`-A` listings are indexed too: each row records its own namespace, so
`kx describe 7` reaches a resource in a namespace you aren't in, and two pods
sharing a name keep separate numbers.

Known kinds can drop the `get` — `kx pods`, `kx deploy -n kube-system`,
`kx svc -m api` — kubectl's shorthands and your CRDs included. A CRD resolves
from kubectl's on-disk discovery cache, with no API call.

`--watch`/`-w` redraws the table in place, rather than appending lines the way
`kubectl -w` does.

`kx completion <bash|zsh|fish|powershell>` completes indexes with the resource
behind them, so `kx describe <TAB>` offers `1  api-7d8f (Pod)` rather than a
bare number.

### Triage a namespace

Bare `kx diag` sweeps the current namespace — every workload kind, plus
Services, PVCs, Ingresses and pods nothing owns — and ranks what's unhealthy.
It reads live usage too, so a pod running hot against its memory limit is
flagged as an OOMKill risk before it dies.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/diag.gif" alt="kx diag demo" width="800"/>
</div>

`kx diag <index>` diagnoses a single resource: a top level verdict, a findings
summary, a per-pod status table, log tails from broken containers and warning
events — one screen instead of four kubectl commands.

[Full guide →](https://jzills.github.io/kx/docs/guides/triage-a-namespace/)

### Read a Secret in plaintext

`kx secret <index> --decode` prints an indexed Secret's keys and values decoded.
`--key`/`-k` prints a single value raw — no banner, no wrapping — so it drops
straight into a shell.

```bash
export PGPASSWORD=$(kx secret 1 --decode -k password)
```

Bare `kx secret --decode` decodes every Secret in the namespace in one call,
confirming first — that prints every credential you have.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/secret.gif" alt="kx secret --decode demo" width="800"/>
</div>

[Full guide →](https://jzills.github.io/kx/docs/guides/read-a-secret/)

### Scan images for CVEs

`kx scan <index>` scans the unique container images of an indexed workload.
Bare `kx scan` sweeps every workload in the namespace. Results come back as a
severity summary, or the full per-image CVE report with `--full`.

Requires the CLI for the selected engine — [Docker Scout](https://docs.docker.com/scout/)
by default, or [Trivy](https://trivy.dev/) and
[Grype](https://github.com/anchore/grype) via `kx engine`.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/scan.gif" alt="kx scan demo" width="800"/>
</div>

[Full guide →](https://jzills.github.io/kx/docs/guides/scan-images/)

### See what owns what

`kx tree <index>` walks the ownership graph — Deployment to ReplicaSet to Pods —
and indexes every node it draws, so anything in the tree is one number away.
Bare `kx tree` graphs the whole namespace.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/tree-html.png" alt="kx tree dashboard" width="800"/>
</div>

[Full guide →](https://jzills.github.io/kx/docs/guides/ownership-tree/)

### Reports in the browser

`--html` on `kx diag`, `kx scan`, `kx tree`, and `kx top` renders the same
analysis as a page and opens it in your browser. It binds `127.0.0.1` only and
writes nothing to disk.

The page is drawn in your active theme. Sweep rows expand into that resource's
full report, image rows into the CVEs behind their counts — detail the terminal
has no room for.

`--out <path>` writes the page to a file instead of serving it, which is what
you want in CI — `kx diag --out report.html` is the whole command.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/diag-html.png" alt="kx diag --html dashboard" width="800"/>
</div>

[Full guide →](https://jzills.github.io/kx/docs/guides/browser-reports/)

## Use kx in CI

`--fail-on <severity>` turns a sweep into a build gate, and `--json` prints the
same analysis as a document for anything downstream.

```bash
kx diag -A --fail-on critical                        # 0 if the cluster is healthy, 2 if not
kx scan -A --fail-on high                            # the same, for image vulnerabilities
kx scan -n prod --fail-on high --json | jq '.images[] | select(.counts.critical > 0)'
kx diag -A --fail-on critical --out report.html      # publishes the report *and* fails the build
kx diag -A --fail-on warning --since 7d              # a week of history, not just today's
```

Only the last 24h counts: a warning event, an OOMKill a container recovered
from, a pod or run that failed. Anything older stops holding the gate red,
while whatever is still going wrong — a CrashLoopBackOff, a Pending pod — is
always reported. `--since` changes the window per run, `diag_max_age` changes
the default.

Exit **2** means findings breached the threshold, **1** means kx itself failed —
so a pipeline can tell "the cluster is sick" from "the check never ran".

[Full guide →](https://jzills.github.io/kx/docs/guides/use-kx-in-ci/)

## State and history

`kx` keeps up to 10 `kx get` results in `~/.kx/state.json`, with a cursor
marking the entry indexes resolve against.

```bash
kx state              # the listing indexes currently resolve against
kx state --all        # the whole history, with positions
kx state 2            # jump to position 2
kx state back         # step back one (forward steps the other way)
kx state drop 2       # remove position 2 (--all clears everything, slots included)
```

Each entry remembers the context it was listed in, so a staging index is never
resolved against production — `kx` refuses and relists instead. `KX_STATE`
points kx at a different state file, so a second terminal keeps its own history.

[Full guide →](https://jzills.github.io/kx/docs/concepts/state/)

## Configuration

`kx` reads `~/.kx/config.toml`, and every setting takes a `KX_*` environment
override. The two worth changing have commands of their own — `kx theme` and
`kx engine` both persist your choice.

Styling is dropped when stdout isn't a terminal, so `kx get pods | grep worker`
stays clean. [`NO_COLOR`](https://no-color.org/) is honored too.

[Full guide →](https://jzills.github.io/kx/docs/concepts/configuration/)

## Themes

`kx theme` lists the available themes with a preview of each. `kx theme <name>`
persists your choice, by name or index.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/theme.gif" alt="kx theme demo" width="800"/>
</div>

Eleven prefabs ship with it: `github-dark` (default), `dracula`, `nord`,
`gruvbox`, `solarized-dark`, `catppuccin-mocha`, `tokyo-night`, `rose-pine`,
`mono`, `light` and `plain`.

[Full guide →](https://jzills.github.io/kx/docs/concepts/themes/)

## Commands

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
| `kx logs <index>...` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services. |
| `kx namespace [<index>]` | List namespaces, or switch to an indexed one; alias: kx ns. |
| `kx port-forward <index> <port>` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx scan [<index>]` | Scan the unique container images of an indexed workload for vulnerabilities, or a whole namespace when no index is given (-n to pick one, -A for every namespace); prints a severity summary table by default, or the raw scanner output with --full. Requires the CLI for the selected scan engine (Docker Scout by default; Trivy or Grype via --engine — see kx engine). |
| `kx secret [<index>...]` | List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets. |
| `kx top [<resource>]` | List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits. |
| `kx tree [<index>]` | Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace. |
| `kx uncordon <index>...` | Mark one or more indexed Nodes schedulable again. |
| `kx yaml <index>...` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx state [<position>]` | Show current state, jump to a history position, list all entries with --all, or expand the switch targets with --targets. |
| `kx engine [<name>]` | List available scan engines or persist a default choice by name or index. |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
| `kx completion` | Generate a shell completion script for kx (bash, zsh, fish, powershell). |
<!-- commands-table-end -->

## Development

Go, at the version pinned by the `go` directive in `go.mod`. Nothing else is
required to build or run.

```bash
go build ./...
go run ./cmd/kx --help              # run the CLI directly
gofmt -l ./cmd ./internal ./tools   # must print nothing
go vet ./...
go test -race ./...
```

`pre-commit run --all-files` runs gofmt and go vet, and regenerates the command
table above from the command tree — it fails if the table has drifted from the
commands it documents. Tests are not in the hook — run them yourself.

The demo GIFs are rendered from [VHS](https://github.com/charmbracelet/vhs)
tapes — see [`demo/README.md`](demo/README.md) for seeding the demo namespace
and re-recording.
