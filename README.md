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

## Contents

- [Install](#install)
- [Usage](#usage)
  - [Commands](#commands)
  - [Triage a namespace](#triage-a-namespace)
  - [Read a Secret in plaintext](#read-a-secret-in-plaintext)
  - [Scan images for vulnerabilities](#scan-images-for-vulnerabilities)
  - [View reports in a browser](#view-reports-in-a-browser)
- [History](#history)
- [Configuration](#configuration)
- [Themes](#themes)
- [Development](#development)

## Install

Requires `kubectl` on your PATH. Every install path below delivers the same
prebuilt binary — no Python runtime, no dependencies.

With [uv](https://docs.astral.sh/uv/) (recommended):

```bash
uv tool install kx-cli
```

With [pipx](https://pipx.pypa.io/):

```bash
pipx install kx-cli
```

With pip:

```bash
pip install kx-cli
```

As a kubectl plugin via [krew](https://krew.sigs.k8s.io/), where kx is published as `idx`:

```bash
kubectl krew install idx
alias kx="kubectl idx"
```

Standalone binaries for linux, macOS and Windows (amd64/arm64) are attached to each [GitHub Release](https://github.com/jzills/kx/releases), with checksums in `SHA256SUMS`.

On macOS, the first run of a freshly installed krew plugin or standalone binary takes a few seconds while Gatekeeper scans the binary; later runs are unaffected until the next install. Get it over with up front:

```bash
kx --version >/dev/null
```

Or try it without installing (the package is `kx-cli`, the command is `kx`):

```bash
uvx --from kx-cli kx get pods
pipx run --spec kx-cli kx get pods
```

## Usage

`kx get <resource>` fetches resources and assigns each row an index; every other command takes those indexes. Extra flags pass through to kubectl (`-n <namespace>`, selectors, ...), and `--match`/`-m` filters rows by name substring. All-namespace listings (`-A`) are display-only — names aren't unique across namespaces.

`--watch` redraws the table live as resources are added, changed, or removed, instead of printing a table that never finishes. It's display-only — a watch never completes, so there's nothing to index. Non-tabular output (`-o json`/`yaml`/etc.) streams kubectl's own watch output directly instead.

Known kinds can drop the `get`: `kx pods`, `kx deploy -n kube-system`, `kx svc --match api` — kubectl shorthands (`po`, `deploy`, `svc`, `sts`, ...) included. An integer after a kind relists just that index: `kx po 3`. CRDs and other resource types still use `kx get <resource>`.

Global flags: `--no-color` disables styled output, `-v`/`--version` prints the installed version, and `-h`/`--help` on any command shows usage, examples, and aliases.

### Commands

<!-- commands-table-start -->
| Command | Description |
|---|---|
| `kx annotate <index> [<key=value>...] [--overwrite] [--remove str]` | Set or remove annotations on an indexed resource. |
| `kx annotations <index>...` | Show annotations for one or more indexed resources. |
| `kx context [<index>]` | List kubeconfig contexts, or switch to an indexed one; alias: kx contexts. |
| `kx cp <src> <dest> [--container/-c str] [--no-preserve] [--retries int] [kubectl flags...]` | Copy files to or from an indexed pod via kubectl cp. |
| `kx delete <index>... [--yes/-y]` | Delete one or more indexed resources (prompts for confirmation unless --yes). |
| `kx describe <index>... [kubectl flags...]` | Show full kubectl describe output for one or more indexed resources. |
| `kx diagnostic [<index>] [--all-namespaces/-A] [--full] [--html] [--namespace/-n str] [--no-open] [--port int]` | Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, or Pod, or triage a whole namespace when no index is given (-n to pick one, -A for every namespace); alias: kx diag. |
| `kx edit <index> [kubectl flags...]` | Open an indexed resource in your editor via kubectl edit. |
| `kx events <index>...` | Show Kubernetes events for one or more indexed resources. |
| `kx exec <index> [<command>...] [kubectl flags...]` | Open an interactive shell in an indexed pod (bash, falling back to sh). |
| `kx get <resource> [<index>...] [--all-namespaces/-A] [--decode] [--key/-k str] [--match/-m str] [--namespace/-n str] [--yes/-y] [kubectl flags...]` | List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3). |
| `kx label <index> [<key=value>...] [--overwrite] [--remove str]` | Set or remove labels on an indexed resource. |
| `kx labels <index>... [--selector/-s]` | Show labels for one or more indexed resources; --selector formats output as a label selector. |
| `kx logs <index>... [kubectl flags...]` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services. |
| `kx namespace [<index>]` | List namespaces, or switch to an indexed one; alias: kx ns. |
| `kx port-forward <index> <port> [kubectl flags...]` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx scan [<index>] [--all-namespaces/-A] [--engine str] [--full] [--html] [--namespace/-n str] [--no-open] [--port int] [scanner flags...]` | Scan the unique container images of an indexed workload for vulnerabilities, or a whole namespace when no index is given (-n to pick one, -A for every namespace); prints a severity summary table by default, or the raw scanner output with --full. Requires the CLI for the selected scan engine (Docker Scout by default, https://docs.docker.com/scout/; or Trivy via --engine trivy, https://trivy.dev/ — see kx engine). |
| `kx secret [<index>...] [--all-namespaces/-A] [--decode] [--key/-k str] [--match/-m str] [--namespace/-n str] [--yes/-y] [kubectl flags...]` | List Secrets like kx get, or show an indexed Secret's data with --decode; alias: kx secrets. |
| `kx top [<resource>] [--all-namespaces/-A] [--html] [--match/-m str] [--namespace/-n str] [--no-limits] [--no-open] [--port int] [kubectl flags...]` | List CPU/memory usage for pods (default) or nodes and assign index numbers, like kx get; shows usage as a percent of limits (pods) or capacity (nodes) unless --no-limits. |
| `kx tree [<index>] [--all-namespaces/-A] [--html] [--namespace/-n str] [--no-index] [--no-open] [--port int]` | Show the ownership graph for an indexed resource, or the whole current namespace when no index is given (-n to pick one, -A for every namespace); assigns indexes to tree nodes by default. A Namespace index graphs that namespace. |
| `kx yaml <index>... [--show str]` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx state [<position>] [--all/-a] [--targets/-t]` | Show current state, jump to a history position, list all entries with --all, or expand the switch targets with --targets. |
| `kx engine [<name>]` | List available scan engines or persist a default choice by name or index. |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
<!-- commands-table-end -->

### Triage a namespace

Bare `kx diag` sweeps the current namespace — Deployments, StatefulSets,
DaemonSets, Jobs, CronJobs, Services, and PersistentVolumeClaims, plus pods
nothing owns — and prints a ranked table of what's unhealthy. Findings also
draw on live resource usage (`kx top`): a pod running hot against its memory
limit is flagged as an OOMKill risk before it dies. The rows are indexed, so
`kx diag 1` or `kx logs 2` drill straight in.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/diag.gif" alt="kx diag demo" width="800"/>
</div>

`kx diag <index>` diagnoses a single resource: a verdict banner, a `SUMMARY`
of findings (CrashLoopBackOff, image pull failures, OOMKills, unschedulable
pods, stalled rollouts, missing Service endpoints, Pending PVCs, failed
CronJob runs, usage near limits), a per-pod status table, recent log tails
from broken containers, and warning events — one screen instead of four
kubectl commands.

### Read a Secret in plaintext

`kx secret <index> --decode` prints an indexed Secret's keys and values
decoded, instead of the base64 kubectl returns. Values that aren't text show a
`<binary, N bytes>` placeholder rather than garbling the table. `--key`/`-k`
prints a single value raw — no banner, no wrapping — so it drops straight into
a shell: `export PGPASSWORD=$(kx secret 1 --decode -k password)`, or redirect a
binary value to a file. Bare `kx secret --decode` decodes every Secret in the
namespace in one call, `-n` included — it confirms first unless you pass
`--yes`/`-y`, since that prints every credential in the namespace.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/secret.gif" alt="kx secret --decode demo" width="800"/>
</div>

### Scan images for vulnerabilities

`kx scan <index>` scans the unique container images of an indexed workload
(init containers and CronJob job templates included); bare `kx scan` sweeps
every workload in the namespace. Results come back as a severity summary,
or the full per-image CVE report with `--full`. Requires the CLI for the
selected engine — [Docker Scout](https://docs.docker.com/scout/) by
default, or [Trivy](https://trivy.dev/) via `kx engine trivy` (see
[Configuration](#configuration)).

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/scan.gif" alt="kx scan demo" width="800"/>
</div>

### View reports in a browser

`kx diag --html`, `kx scan --html`, `kx tree --html`, and `kx top --html`
render the same analysis as a page and open it in your browser as well as
printing to the terminal; Ctrl-C stops the server. It binds `127.0.0.1`
only, and nothing is written to disk — the report lives in memory for as
long as the command runs.

The page is drawn in your active theme, so `kx theme dracula` restyles it too.
Sweep rows expand into that resource's full report, and image rows expand
into the CVEs behind their severity counts — detail the terminal has no room
for. None of it costs an extra API or scanner call: both were already
gathered to build the table you'd see without `--html`.

`--port` serves on a specific port instead of picking a free one, and
`--no-open` skips launching a browser — the URL still prints, so you can
open it yourself.

#### `kx diag --html`

A namespace sweep's severity-sorted findings, filterable and sortable by
any column, with a group-by for larger sweeps.

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

The ownership graph as a collapsible tree, indexed like every other kx
listing.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/tree-html.png" alt="kx tree --html dashboard" width="800"/>
</div>

## History

`kx` maintains a history of up to 10 `kx get` results in `~/.kx/state.json`. A cursor tracks your current position; index-based commands resolve against the entry at the cursor. `kx state --all` lists the history, `kx state <position>` jumps to an entry, `kx state back`/`kx state forward` step through it, and `kx state drop <position>` removes one (`kx state drop --all` clears everything, including the namespace/context slots below). The older `kx back`/`kx forward`/`kx drop` spellings still work too.

Namespaces and contexts are kept separately, outside that history. `kx ns` and `kx contexts` each save their listing to their own slot, so `kx ns 2` counts against the namespaces you last listed no matter what you have listed since — and switching namespaces, which is frequent, never pushes work out of the history. `kx state --all` summarizes those slots under the history table, and `kx state --targets` expands them to the indexed listings the switch commands read, so you can pick a number without re-listing.

To operate on a namespace rather than switch to it, list it like any other resource with `kx get ns`; that puts it in the history too, so `kx describe <index>` and `kx label <index>` work as usual. It refreshes the slot as well, so the two spellings never disagree about what index 2 means. A narrowed listing counts, though: after `kx get ns -l team=platform`, `kx ns <index>` indexes into those namespaces rather than all of them. Run `kx ns` to list them all again.

## Configuration

`kx` reads `~/.kx/config.toml`; environment variables override file settings.

| Key | Env var | Default | Description |
| --- | --- | --- | --- |
| `max_history` | `KX_MAX_HISTORY` | `10` | Number of `kx get` results kept in history. |
| `shells` | `KX_SHELLS` (comma-separated) | `["bash", "sh"]` | Shell candidates for `kx exec`. |
| `no_color` | `KX_NO_COLOR` | `false` | Disable styled output (same as `--no-color`). |
| `theme` | `KX_THEME` | `"github-dark"` | Color theme for all output. |

Styled output is emitted only when stdout is a terminal — piped or redirected output is plain text, so `kx get pods | grep worker` stays clean. The [`NO_COLOR`](https://no-color.org/) convention is honored as well.

## Themes

`kx theme` lists the available themes with a preview of each; `kx theme <name|index>` persists a choice to `~/.kx/config.toml`.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/theme.gif" alt="kx theme demo" width="800"/>
</div>

Prefab themes: `github-dark` (default), `dracula`, `nord`, `gruvbox`, `solarized-dark`, `catppuccin-mocha`, `tokyo-night`, `rose-pine`, `mono`, `light` (for light terminal backgrounds), and `plain` (no styling at all).

## Development

Go, at the version pinned by the `go` directive in `go.mod`. Nothing else is
required to build or run.

```bash
go build ./...
```

Run the CLI directly:

```bash
go run ./cmd/kx --help
go run ./cmd/kx get pods
```

Checks:

```bash
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
