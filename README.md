<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/assets/banner.svg" alt="kx — kubectl, indexed." width="800"/>
</div>

<br>

<div align="center">

# kubectl, indexed

</div>

<div align="center">

[![PyPI version](https://img.shields.io/pypi/v/kx-cli?style=flat-square&color=3fb950&labelColor=21262d)](https://pypi.org/project/kx-cli/)
[![Python](https://img.shields.io/pypi/pyversions/kx-cli?style=flat-square&color=3fb950&labelColor=21262d)](https://pypi.org/project/kx-cli/)
[![License](https://img.shields.io/github/license/jzills/kx?style=flat-square&color=3fb950&labelColor=21262d)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/jzills/kx/pr.yml?style=flat-square&color=3fb950&labelColor=21262d&label=CI)](https://github.com/jzills/kx/actions/workflows/pr.yml)

</div>

`kx` is a kubectl wrapper that adds index-based resource selection. Run `kx get <resource>` once, then reference any result by number instead of typing full resource names.

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/demo.gif" alt="kx demo" width="800"/>
</div>

## Install

Requires Python 3.11+ and `kubectl` on your PATH.

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

As a kubectl plugin via [krew](https://krew.sigs.k8s.io/), where kx is published as `idx` (no Python required):

```bash
kubectl krew install idx
alias kx="kubectl idx"
```

Standalone binaries (linux/macOS, amd64/arm64, no Python required) are attached to each [GitHub Release](https://github.com/jzills/kx/releases).

Or try it without installing (the package is `kx-cli`, the command is `kx`):

```bash
uvx --from kx-cli kx get pods
pipx run --spec kx-cli kx get pods
```

## Usage

`kx get <resource>` fetches resources and assigns each row an index; every other command takes those indexes. Extra flags pass through to kubectl (`-n <namespace>`, selectors, ...), and `--match`/`-m` filters rows by name substring. All-namespace listings (`-A`) are display-only — names aren't unique across namespaces.

Known kinds can drop the `get`: `kx pods`, `kx deploy -n kube-system`, `kx svc --match api` — kubectl shorthands (`po`, `deploy`, `svc`, `sts`, ...) included. An integer after a kind relists just that index: `kx po 3`. CRDs and other resource types still use `kx get <resource>`.

Global flags: `--no-color` disables styled output, `-v`/`--version` prints the installed version, and `--help` on any command shows usage, examples, and aliases.

### Commands

<!-- commands-table-start -->
| Command | Description |
|---|---|
| `kx get <resource> [--match/-m str] [kubectl flags...]` | List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods, kx po 3). |
| `kx top [--match/-m str] [--no-limits] [kubectl flags...]` | List CPU/memory usage for pods in the current namespace and assign index numbers, like kx get; shows usage as a percent of each pod's resource limits unless --no-limits. |
| `kx describe <indexes>... [kubectl flags...]` | Show full kubectl describe output for one or more indexed resources. |
| `kx events <indexes>...` | Show Kubernetes events for one or more indexed resources. |
| `kx logs <index> [kubectl flags...]` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services. |
| `kx scan [<index>] [--engine str] [--full] [kubectl flags...]` | Scan the unique container images of an indexed workload for vulnerabilities, or the whole namespace when no index is given; prints a severity summary table by default, or the raw scanner output with --full. |
| `kx labels <indexes>... [--selector/-s]` | Show labels for one or more indexed resources; --selector formats output as a label selector. |
| `kx annotations <indexes>...` | Show annotations for one or more indexed resources. |
| `kx label <index> [<pairs>...] [--remove str] [--overwrite]` | Set or remove labels on an indexed resource. |
| `kx annotate <index> [<pairs>...] [--remove str] [--overwrite]` | Set or remove annotations on an indexed resource. |
| `kx yaml <indexes>... [--show str]` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx delete <indexes>... [--yes/-y]` | Delete one or more indexed resources (prompts for confirmation unless --yes). |
| `kx edit <index> [kubectl flags...]` | Open an indexed resource in your editor via kubectl edit. |
| `kx exec <index> [<cmd>...] [kubectl flags...]` | Open an interactive shell in an indexed pod (bash, falling back to sh). |
| `kx tree <index> [--index/-i]` | Show the ownership graph for an indexed resource; --index assigns indexes to tree nodes. |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx port-forward <index> <port> [kubectl flags...]` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx diagnostic [<index>]` | Diagnose an indexed Deployment, StatefulSet, DaemonSet, Job, CronJob, Service, PersistentVolumeClaim, or Pod, or triage the whole namespace when no index is given; alias: kx diag. |
| `kx namespace [<index>]` | List namespaces, or switch to an indexed one; alias: kx ns. |
| `kx context [<index>]` | List kubeconfig contexts, or switch to an indexed one; alias: kx contexts. |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
| `kx state [<position>] [--all/-a]` | Show current state, jump to a history position, or list all entries with --all. |
| `kx drop <position>` | Remove a history entry by position (shown in kx state --all). |
| `kx back` | Navigate to the previous kx get result. |
| `kx forward` | Navigate to the next kx get result. |
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

### Scan images for vulnerabilities

`kx scan <index>` scans the unique container images of an indexed workload
(init containers and CronJob job templates included); bare `kx scan` sweeps
every workload in the namespace. Results come back as a severity summary,
or the full per-image CVE report with `--full`. Requires
[Docker Scout](https://docs.docker.com/scout/).

<div align="center">
  <img src="https://raw.githubusercontent.com/jzills/kx/main/demo/scan.gif" alt="kx scan demo" width="800"/>
</div>

## State

`kx` maintains a history of up to 10 `kx get` results in `~/.kx/state.json`. A cursor tracks your current position; index-based commands always resolve against the entry at the cursor. `kx state --all` lists the history, `kx state <position>` jumps to an entry, `kx back`/`kx forward` step through it, and `kx drop <position>` removes one.

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

```bash
python -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

Run the CLI directly:

```bash
python -m kx.main --help
```

The demo GIFs are rendered from [VHS](https://github.com/charmbracelet/vhs)
tapes — see [`demo/README.md`](demo/README.md) for seeding the demo namespace
and re-recording.
