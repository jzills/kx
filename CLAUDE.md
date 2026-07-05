# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Environment

Python 3.11+ (CI tests 3.11–3.13), virtual environment at `.venv/`.

```bash
source .venv/bin/activate
pip install -e ".[dev]"
```

## Running the CLI

```bash
python -m kx.main --help
python -m kx.main <command> [args]
```

After installing (`pip install -e .`), the `kx` entrypoint is available directly.

## Linting

```bash
ruff check src/
```

## Stack

- **Typer** — CLI framework; commands defined in `main.py` with injected dependencies
- **kubernetes** — Python SDK used in `events.py`, `graph.py`, `k8s.py` for live cluster calls
- **Rich** — used in `graph.py` for `Tree` rendering; available elsewhere

## Architecture

`kx` is a kubectl wrapper adding index-based resource selection. The workflow: `kx get <resource>` lists resources and saves state; all other commands resolve a numeric index back to a resource name from that saved state.

**State flow:** `kx get` → `kubectl get` output → `IndexService.add()` parses the NAME column and assigns 1-based indexes → a `State` (a `resources` mapping of name→kind, plus `namespace`) is persisted to `~/.kx/state.json` via `StateService.save()`. State is stored as a history stack with a cursor (up to `max_history` entries); subsequent commands call `StateService.fields(index)`, which loads the current entry and resolves the index to `(name, namespace, kind)`.

**Command pattern:** Each command in `src/kx/commands/` is a class constructed in `main.py` with Protocol-typed service objects (`KubectlService`, `StateService`, `IndexService`, `EventsService`) — plus a few plain callables where useful (`confirm` for `delete`; `build_tree`/`build_indexed_tree` for `tree`). Commands depend on the Protocol interfaces (`KubectlServiceProtocol`, etc.), so tests substitute mocks/fakes without subprocess or filesystem side-effects. The Typer command functions are wrapped by the `handle_errors` decorator (`errors.py`), which renders `RuntimeError`/`ValueError` as styled errors and exits 1 (letting `typer.Exit`/`typer.Abort` pass through).

**Two kubectl wrappers** (`KubectlService` in `kubectl.py`):
- `run(args)` — captures stdout, returns a string; raises `RuntimeError` on a non-zero exit (used for `get`, `logs`, `yaml`, `delete`, `scale`, `rollout`)
- `run_interactive(args)` — streams stdio through to the terminal (used for `describe`, `exec`, `edit`, `port-forward`)

  Plus `probe(args)` (silent, returns an exit code — used by `exec` shell detection) and `current_namespace()`.

**Kubernetes SDK usage** (`events.py`, `graph.py`): `load_config()` in `k8s.py` tries `load_kube_config()` then falls back to `load_incluster_config()`. The `tree` command uses the Python SDK directly (not kubectl) to walk ownership references across Deployment → ReplicaSet → Pod → Container.

**`normalize_kind()`** in `kinds.py` maps kubectl shorthand (e.g. `pods`, `deploy`, `svc`) to canonical Kubernetes kind names used in event `involved_object.kind` comparisons.

## Release

Releases are triggered by pushing a `release/vX.Y.Z` branch. CI extracts the version, stamps `pyproject.toml`, builds, publishes to PyPI, and opens a PR back to `main`.
