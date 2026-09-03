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

`kx` is a kubectl wrapper that adds index-based resource selection. Run `kx get <resource>` once, then reference any result by number instead of typing full resource names.

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

Standalone binaries for linux, macOS and Windows (amd64/arm64) are attached to
each [GitHub Release](https://github.com/jzills/kx/releases), with checksums in
`SHA256SUMS`. On macOS, the first run of a freshly installed krew plugin or
standalone binary takes a few seconds while Gatekeeper scans it — run
`kx --version >/dev/null` to get it over with.

## Quickstart

```bash
kx get pods    # lists pods, numbering each row
kx logs 3      # the third pod
kx diag 3      # why it's unhealthy
kx delete 3 5  # or several at once
```

## Usage

Every command takes the numbers `kx get` assigned.

```bash
kx delete 3 5                # several at once
kx delete 3..7               # an inclusive range, walking either direction
kx delete ..5                # open at the start
kx delete 5..                # open to the end of the listing
kx get pods -m api           # --match/-m filters rows by name substring
kx get pods -n prod -l app=api   # anything else passes through to kubectl
```

`-A` listings are indexed too: each row records its own namespace, so
`kx describe 7` reaches a resource in a namespace you aren't in, and two pods
sharing a name keep separate numbers.

Known kinds can drop the `get` — `kx pods`, `kx deploy -n kube-system`,
`kx svc -m api` — kubectl shorthands (`po`, `deploy`, `svc`, `sts`, ...) and
CRDs (short name, kind, or plural) included; CRDs resolve from kubectl's
on-disk discovery cache, with no API call. An integer after a kind relists just
that index (`kx po 3`), and anything unrecognized falls back to
`kx get <resource>`.

`--watch`/`-w` redraws the table live instead of printing one that never
finishes. It's display-only — a watch never completes, so there's nothing to
index; `-o json`/`yaml` stream kubectl's own watch instead.

`--no-color` disables styling, `-v`/`--version` prints the version and build,
and `-h`/`--help` on any command shows usage, examples, and aliases.

`kx completion <bash|zsh|fish|powershell>` prints a completion script. Indexes
complete with the resource behind them — `kx describe <TAB>` offers
`1  api-7d8f (Pod)` — as do resource types, rollout actions, themes, engines,
and `-n` namespaces, all served from `~/.kx/state.json` with no API call.

### Triage a namespace

Bare `kx diag` sweeps the current namespace — Deployments, StatefulSets,
DaemonSets, Jobs, CronJobs, Services, PersistentVolumeClaims, Ingresses, and
pods nothing owns — and ranks what's unhealthy. It reads live usage too, so a
pod running hot against its memory limit is flagged as an OOMKill risk before
it dies. Rows are indexed, so `kx diag 1` or `kx logs 2` drill straight in;
`-A` sweeps every namespace, adding a NAMESPACE column.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/diag.gif" alt="kx diag demo" width="800"/>
</div>

`kx diag <index>` diagnoses one resource: a verdict banner, a `SUMMARY` of
findings (CrashLoopBackOff, image pull failures, OOMKills, unschedulable pods,
stalled rollouts, missing Service endpoints, Pending PVCs, failed CronJob runs,
Ingresses pointing at missing Services, usage near limits), a per-pod status
table, log tails from broken containers, and warning events — one screen
instead of four kubectl commands.

Nodes work the same way, indexed from `kx get nodes` or `kx top nodes`:
conditions (not ready, memory/disk/PID pressure, network unavailable), whether
it's cordoned, and a tally of stuck pods — finished ones excluded, since their
objects linger until garbage collection and would keep a healthy node looking
sick. Nodes are cluster-scoped, so they never appear in a namespace sweep or
`-A`.

### Take a node out of service

`kx cordon <index>` marks an indexed node unschedulable so nothing new lands on
it; `kx drain <index>` also evicts what's already there, streaming kubectl's
progress and prompting first unless you pass `--yes`; `kx uncordon <index>`
puts it back. Cordon and uncordon take several indexes and ranges like
`kx delete`; drain takes one, deliberately — a range is a way to take a cluster
down by typo. kubectl's own drain flags pass through (`--ignore-daemonsets`,
`--delete-emptydir-data`, ...).

### Read a Secret in plaintext

`kx secret <index> --decode` prints an indexed Secret's keys and values decoded,
instead of the base64 kubectl returns. Values that aren't text show a
`<binary, N bytes>` placeholder rather than garbling the table. `--key`/`-k`
prints a single value raw — no banner, no wrapping — so it drops straight into
a shell:

```bash
export PGPASSWORD=$(kx secret 1 --decode -k password)
```

Bare `kx secret --decode` decodes every Secret in the namespace in one call,
`-n` included. It confirms first unless you pass `--yes`/`-y`, since that
prints every credential in the namespace.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/secret.gif" alt="kx secret --decode demo" width="800"/>
</div>

### Scan images for vulnerabilities

`kx scan <index>` scans the unique container images of an indexed workload
(init containers and CronJob job templates included); bare `kx scan` sweeps
every workload in the namespace. Results come back as a severity summary, or
the full per-image CVE report with `--full`. Requires the CLI for the selected
engine — [Docker Scout](https://docs.docker.com/scout/) by default, or
[Trivy](https://trivy.dev/) and [Grype](https://github.com/anchore/grype) via
`kx engine trivy` / `kx engine grype`.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/scan.gif" alt="kx scan demo" width="800"/>
</div>

### View reports in a browser

`--html` on `kx diag`, `kx scan`, `kx tree`, and `kx top` renders the same
analysis as a page and opens it in your browser as well as printing to the
terminal; Ctrl-C stops the server. It binds `127.0.0.1` only and writes nothing
to disk. The page is drawn in your active theme, and costs no extra API or
scanner call — the data was already gathered for the table. Sweep rows expand
into that resource's full report, image rows into the CVEs behind their counts.

`--port` serves on a given port instead of a free one; `--no-open` skips the
browser but still prints the URL. `--out <path>` writes the page to a file
instead of serving it — the one case a report reaches disk — and implies
`--html`, so `kx diag --out report.html` is the whole command; `--port` and
`--no-open` are refused alongside it, since they configure a server it never
starts.

#### `kx diag --html`

A namespace sweep's severity-sorted findings, filterable and sortable by any
column, with a group-by for larger sweeps.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/diag-html.png" alt="kx diag --html dashboard" width="800"/>
</div>

#### `kx scan --html`

Per-image severity counts up top; the CVE table below groups by image and
expands each row into its full detail.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/scan-html.png" alt="kx scan --html dashboard" width="800"/>
</div>

#### `kx tree --html`

The ownership graph as a collapsible tree, indexed like every other kx listing.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/tree-html.png" alt="kx tree --html dashboard" width="800"/>
</div>

## Use kx in CI

`kx diag`, `kx scan`, `kx tree` and `kx top` all take `--json`: the same
analysis as a machine-readable document, every swept resource — healthy ones
included — and every CVE behind the counts. Each carries a `schemaVersion` and
names its subject in fields, so a consumer never has to split `Deployment/api`
apart, and all three views are built from the same values, so they can't
disagree.

`--fail-on <severity>` turns the two that produce verdicts into a gate:

```bash
kx diag -A --fail-on critical                        # 0 if the cluster is healthy, 2 if not
kx scan -A --fail-on high                            # the same, for image vulnerabilities
kx scan -n prod --fail-on high --json | jq '.images[] | select(.counts.critical > 0)'
kx diag -A --fail-on critical --out report.html      # publishes the report *and* fails the build
```

`kx tree` and `kx top` have no `--fail-on` — neither produces a verdict to gate
on. The gate applies alongside `--json` and `--out` alike, so a pipeline can
publish a report and still fail on what's in it. Use `--out`, not bare `--html`,
in CI: `--html` blocks until Ctrl-C, which a pipeline never sends, so the gate
after it never runs. `kx scan --full --fail-on` is refused rather than ignored,
since `--full` streams the scanner's own report and kx never parses it.

Exit **2** means findings breached the threshold; **1** means kx itself failed —
so a pipeline can tell "the cluster is sick" from "the check never ran". An
image kx couldn't scan breaches every threshold for the same reason.

## History

`kx` keeps up to 10 `kx get` results in `~/.kx/state.json`, with a cursor
marking the entry indexes resolve against.

```bash
kx state              # the listing indexes currently resolve against
kx state --all        # the whole history, with positions
kx state 2            # jump to position 2
kx state back         # step back one (forward steps the other way)
kx state drop 2       # remove position 2 (--all clears everything, slots included)
```

The older `kx back`/`kx forward`/`kx drop` spellings still work. `KX_STATE`
points kx at a different state file, so a second terminal — or a CI job — keeps
its own history instead of sharing the one in `~/.kx`.

Each entry records the context it was listed in, since a resource name means
nothing without its cluster — `kx state` names it beside the namespace, and
`kx state --all` gives it a column when the history spans more than one.
Switching contexts therefore retires your indexes: rather than resolve a staging
index against production, `kx` refuses, names both contexts, and re-runs the
listing so there are usable numbers on screen — `kx get pods` in staging,
`kx context 2`, then `kx delete 1` deletes nothing and relists. `kx ns <index>`
is refused the same way; `kx context <index>` is the exception, since contexts
live in kubeconfig rather than in any cluster.

`kx ns` and `kx contexts` save to slots of their own, outside that history, so
`kx ns 2` counts against the namespaces you last listed however much you've
listed since — and switching namespaces never pushes work off the stack.
`kx state --all` summarizes the slots; `kx state --targets` expands them so you
can pick a number without re-listing.

To operate on a namespace rather than switch to it, list it with `kx get ns`:
that stacks it, so `kx describe <index>` and `kx label <index>` work as usual,
and refreshes the slot so both spellings agree on what `2` means. A narrowed
listing counts — after `kx get ns -l team=platform`, `kx ns <index>` indexes
those. Run `kx ns` for all of them again.

## Configuration

`kx` reads `~/.kx/config.toml`; environment variables override file settings.

| Key | Env var | Default | Description |
| --- | --- | --- | --- |
| `max_history` | `KX_MAX_HISTORY` | `10` | Number of `kx get` results kept in history. |
| `shells` | `KX_SHELLS` (comma-separated) | `["bash", "sh"]` | Shell candidates for `kx exec`. |
| `debug_image` | `KX_DEBUG_IMAGE` | `"busybox"` | Image `kx debug` attaches to a pod, or runs on a node; `--image` overrides it. |
| `theme` | `KX_THEME` | `"github-dark"` | Color theme for all output. |
| `theme_disable` | `KX_THEME_DISABLE` | `false` | Disable styled output (same as `--no-color`). |
| `engine` | `KX_ENGINE` | `"scout"` | Default scan engine for `kx scan` (`scout`, `trivy`, `grype`). |

`KX_CONFIG` points kx at a config file other than `~/.kx/config.toml`, the way
`KX_STATE` does for the state file.

Styled output is emitted only when stdout is a terminal — piped or redirected
output is plain text, so `kx get pods | grep worker` stays clean. The
[`NO_COLOR`](https://no-color.org/) convention is honored as well.

## Themes

`kx theme` lists the available themes with a preview of each; `kx theme <name|index>` persists a choice to `~/.kx/config.toml`.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/theme.gif" alt="kx theme demo" width="800"/>
</div>

Prefab themes: `github-dark` (default), `dracula`, `nord`, `gruvbox`, `solarized-dark`, `catppuccin-mocha`, `tokyo-night`, `rose-pine`, `mono`, `light` (for light terminal backgrounds), and `plain` (no styling at all).

## Commands

<!-- commands-table-start -->
| Command | Description |
|---|---|
| `kx annotate <index> [<key=value>...] [--overwrite] [--remove str]` | Set or remove annotations on an indexed resource. |
| `kx annotations <index>...` | Show annotations for one or more indexed resources. |
| `kx context [<index>]` | List kubeconfig contexts, or switch to an indexed one; alias: kx contexts. |
| `kx cordon <index>...` | Mark one or more indexed Nodes unschedulable. |
| `kx cp <src> <dest> [--container/-c str] [--no-preserve] [--retries int] [kubectl flags...]` | Copy files to or from an indexed pod via kubectl cp. |
| `kx debug <index> [<command>...] [--image str] [--target str] [kubectl flags...]` | Open a debug shell on an indexed Pod (an ephemeral container, for images with no shell) or Node (a privileged pod on the host). |
| `kx delete <index>... [--yes/-y]` | Delete one or more indexed resources (prompts for confirmation unless --yes). |
| `kx describe <index>... [kubectl flags...]` | Show full kubectl describe output for one or more indexed resources. |
| `kx diagnostic [<index>] [--all-namespaces/-A] [--fail-on str] [--full] [--html] [--json] [--namespace/-n str] [--no-open] [--out str] [--port int]` | Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, Ingress, Pod, or Node, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag. |
| `kx drain <index> [--delete-emptydir-data] [--force] [--grace-period int] [--ignore-daemonsets] [--timeout duration] [--yes/-y] [kubectl flags...]` | Evict the pods from an indexed Node (prompts for confirmation unless --yes). |
| `kx edit <index> [kubectl flags...]` | Open an indexed resource in your editor via kubectl edit. |
| `kx events <index>...` | Show Kubernetes events for one or more indexed resources. |
| `kx exec <index> [<command>...] [kubectl flags...]` | Open an interactive shell in an indexed Pod, Deployment, ReplicaSet, StatefulSet or DaemonSet (bash, falling back to sh). |
| `kx get <resource> [<index>...] [--all-namespaces/-A] [--decode] [--key/-k str] [--match/-m str] [--namespace/-n str] [--watch/-w] [--yes/-y] [kubectl flags...]` | List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3). |
| `kx label <index> [<key=value>...] [--overwrite] [--remove str]` | Set or remove labels on an indexed resource. |
| `kx labels <index>... [--selector/-s]` | Show labels for one or more indexed resources; --selector formats output as a label selector. |
| `kx logs <index>... [kubectl flags...]` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services. |
| `kx namespace [<index>]` | List namespaces, or switch to an indexed one; alias: kx ns. |
| `kx port-forward <index> <port> [kubectl flags...]` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx scan [<index>] [--all-namespaces/-A] [--engine str] [--fail-on str] [--full] [--html] [--json] [--namespace/-n str] [--no-open] [--out str] [--port int] [scanner flags...]` | Scan the unique container images of an indexed workload for vulnerabilities, or a whole namespace when no index is given (-n to pick one, -A for every namespace); prints a severity summary table by default, or the raw scanner output with --full. Requires the CLI for the selected scan engine (Docker Scout by default; Trivy or Grype via --engine — see kx engine). |
| `kx secret [<index>...] [--all-namespaces/-A] [--decode] [--key/-k str] [--match/-m str] [--namespace/-n str] [--watch/-w] [--yes/-y] [kubectl flags...]` | List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets. |
| `kx top [<resource>] [--all-namespaces/-A] [--html] [--json] [--match/-m str] [--namespace/-n str] [--no-limits] [--no-open] [--out str] [--port int] [kubectl flags...]` | List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits. |
| `kx tree [<index>] [--all-namespaces/-A] [--html] [--json] [--namespace/-n str] [--no-index] [--no-open] [--out str] [--port int]` | Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace. |
| `kx uncordon <index>...` | Mark one or more indexed Nodes schedulable again. |
| `kx yaml <index>... [--show str]` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx state [<position>] [--all/-a] [--targets/-t]` | Show current state, jump to a history position, list all entries with --all, or expand the switch targets with --targets. |
| `kx engine [<name>]` | List available scan engines or persist a default choice by name or index. |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
| `kx completion` | Generate a shell completion script for kx (bash, zsh, fish, powershell). |
<!-- commands-table-end -->

## Development

Go, at the version pinned by the `go` directive in `go.mod`. Nothing else is
required to build or run.

```bash
go build ./...
go run ./cmd/kx --help          # run the CLI directly
gofmt -l ./cmd ./internal ./tools   # must print nothing
go vet ./...
go test -race ./...
```

`pre-commit run --all-files` runs gofmt and go vet, and regenerates the command
table above from the command tree — it fails if the table has drifted from the
commands it documents. Tests are not in the hook; run them yourself.

The demo GIFs are rendered from [VHS](https://github.com/charmbracelet/vhs)
tapes — see [`demo/README.md`](demo/README.md) for seeding the demo namespace
and re-recording.
