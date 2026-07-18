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

```bash
pip install kx-cli
```

## Usage

### List resources

```
kx get <resource> [--match|-m <substring>] [kubectl flags...]
```

Fetches resources and assigns index numbers. Any extra flags (e.g. `-n <namespace>`, `-A`) are passed through to kubectl. Use `--match`/`-m` to filter results by name (substring, case-insensitive).

Known kinds can drop the `get`: `kx pods`, `kx deploy -n kube-system`, `kx svc --match api`. This covers the built-in kinds and their kubectl shorthands (`po`, `deploy`, `svc`, `sts`, ...); existing commands take precedence, so `kx ns 3` still switches namespaces (use `kx namespaces` to list them). CRDs and other resource types still use `kx get <resource>`.

```
$ kx get pods
Pods · default · 3 items
  X    NAME                      READY    STATUS     RESTARTS    AGE
  1    api-7d9f4b8c6-xkp2q       1/1      Running    0            2d
  2    worker-6c8b5f7d9-mnt4r    1/1      Running    3            5h
  3    postgres-0                1/1      Running    0           12d
```

All subsequent commands reference resources by their `X` index from the last `kx get`.

Global flags: `--no-color` disables styled output, `--version` prints the installed version, and `--help` on any command shows usage, examples, and aliases.

### Commands

<!-- commands-table-start -->
| Command | Description |
|---|---|
| `kx get <resource> [--match/-m text] [kubectl flags...]` | List resources and assign index numbers for use with other commands; shorthand: kx <kind> (e.g. kx pods). |
| `kx describe <indexes>... [kubectl flags...]` | Show full kubectl describe output for one or more indexed resources. |
| `kx events <indexes>...` | Show Kubernetes events for one or more indexed resources. |
| `kx logs <index> [kubectl flags...]` | Stream logs for an indexed resource; aggregates across pods for Deployments, StatefulSets, DaemonSets, and Services. |
| `kx labels <indexes>... [--selector/-s]` | Show labels for one or more indexed resources; --selector formats output as a label selector. |
| `kx yaml <indexes>... [--show text]` | Print the raw YAML manifest for one or more indexed resources; --show filters to specific top-level fields. |
| `kx delete <indexes>... [--yes/-y]` | Delete one or more indexed resources (prompts for confirmation unless --yes). |
| `kx edit <index> [kubectl flags...]` | Open an indexed resource in your editor via kubectl edit. |
| `kx exec <index> [<cmd>...] [kubectl flags...]` | Open an interactive shell in an indexed pod (bash, falling back to sh). |
| `kx tree <index> [--index/-i]` | Show the ownership graph for an indexed resource; --index assigns indexes to tree nodes. |
| `kx rollout <action> <index>` | Run a rollout action (status, restart, pause, resume, history, undo) on a Deployment, StatefulSet, or DaemonSet. |
| `kx scale <index> <replicas>` | Scale an indexed Deployment, StatefulSet, or ReplicaSet to a given replica count. |
| `kx port-forward <index> <port> [kubectl flags...]` | Forward a local port to an indexed resource (Pod, Deployment, ReplicaSet, StatefulSet, DaemonSet, Service). |
| `kx diagnostic <index>` | Run read-only health diagnostics on an indexed Deployment, StatefulSet, DaemonSet, or Pod; alias: kx diag. |
| `kx namespace <index>` | Switch to an indexed namespace; alias: kx ns (run kx get namespaces first). |
| `kx theme [<name>]` | List available color themes or persist a choice by name or index. |
| `kx state [<position>] [--all/-a]` | Show current state, jump to a history position, or list all entries with --all. |
| `kx drop <position>` | Remove a history entry by position (shown in kx state --all). |
| `kx back` | Navigate to the previous kx get result. |
| `kx forward` | Navigate to the next kx get result. |
<!-- commands-table-end -->

### Example workflow

```bash
# list deployments, pick index 2
kx get deployments
kx describe 2

# check events on that deployment
kx events 2

# drill into a pod
kx get pods
kx logs 1
kx exec 1            # opens bash/sh
kx exec 1 -- env     # run a specific command

# forward local port 8080 to port 80 on a service
kx get services
kx port-forward 2 8080:80

# navigate history after multiple gets
kx get pods
kx get deployments
kx logs 1            # logs from pod index 1
kx state --all       # review full history

# clean up
kx delete 3
```

## State

`kx` maintains a history of up to 10 `kx get` results in `~/.kx/state.json`. A cursor tracks your current position; index-based commands always resolve against the entry at the cursor.

```
$ kx get pods          # saves a new entry, cursor advances
$ kx get deployments   # saves another entry, cursor advances
$ kx logs 1            # resolves index 1 from the pods result
$ kx state --all       # lists all history entries and the current position
```

Use `kx state <position>` to jump directly to any history entry, and `kx drop <position>` to remove one.

## Configuration

`kx` reads `~/.kx/config.toml`; environment variables override file settings.

| Key | Env var | Default | Description |
| --- | --- | --- | --- |
| `max_history` | `KX_MAX_HISTORY` | `10` | Number of `kx get` results kept in history. |
| `shells` | `KX_SHELLS` (comma-separated) | `["bash", "sh"]` | Shell candidates for `kx exec`. |
| `no_color` | `KX_NO_COLOR` | `false` | Disable styled output (same as `--no-color`). |
| `theme` | `KX_THEME` | `"github-dark"` | Color theme for all output. |

Styled output is emitted only when stdout is a terminal — piped or redirected output is plain text, so `kx get pods | grep worker` stays clean. The [`NO_COLOR`](https://no-color.org/) convention is honored as well.

### Themes

```bash
kx theme        # list available themes (indexed) with a preview of each
kx theme nord   # persist a theme to ~/.kx/config.toml
kx theme 3      # same, selecting by index from the kx theme listing
```

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
