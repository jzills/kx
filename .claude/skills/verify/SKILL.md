---
name: verify
description: How to run and verify kx CLI changes end-to-end against the local test cluster
---

# Verifying kx changes

## Run

```bash
source .venv/bin/activate
python -m kx.main <command> [args]
```

Spinners, colors, and Live regions only render on a terminal — wrap invocations
in a pty to observe them:

```bash
script -qec "python -m kx.main get pods" /dev/null
```

Piped invocations (`python -m kx.main get pods | cat`) exercise the
non-terminal path: no spinner frames, no color.

## Test cluster

The local docker-desktop context has a `diagnostics` namespace seeded with
intentionally broken workloads (ImagePullBackOff, CrashLoopBackOff, OOM,
Pending, not-ready) — ideal for status colors, events, and `kx diagnostic`.
`web-healthy` is the healthy Deployment; its pods are safe to delete (the
ReplicaSet recreates them).

## Flows worth driving

- `kx get pods` → indexed table, status colors, spinner
- `kx logs <deploy-index> --tail=1` → selector-lookup spinner, then streaming
- `kx delete <pod-index>` fed `printf 'y\n'` → confirm prompt must render
  *before* any spinner (prompts inside Live regions break input)
- `kx describe 99` → styled error path (✗ in the error color)
- `kx ns <index>` → **mutates your kubeconfig namespace — switch back to
  `diagnostics` after** (`kx get namespaces` then `kx ns <its index>`)

## Gotchas

- zsh expands bare `=word` — quote separators like `"===LOGS==="` in
  compound commands.
- The pty from `script` inherits the current terminal width; long resource
  names truncate with `…` — that's Rich, not a bug.
