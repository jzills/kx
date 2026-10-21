---
name: verify
description: Use when running, demoing or verifying a kx CLI change end-to-end against a real cluster rather than only in tests. Covers building the binary, isolating state with KX_STATE/KX_CONFIG, seeding the diagnostics namespace, scenarios, the flows worth driving, and the exit codes to assert on.
---

# Verifying kx changes

## Run

```bash
go run ./cmd/kx <command> [args]
```

For anything comparing behaviour across commits, build instead — `go run`
recompiles each invocation and the difference swamps short commands:

```bash
go build -o /tmp/kx ./cmd/kx && /tmp/kx get pods
```

Spinners and colors only render on a terminal, so wrap invocations in a pty to
observe them:

```bash
script -qec "go run ./cmd/kx get pods" /dev/null
```

Piped invocations (`go run ./cmd/kx get pods | cat`) exercise the non-terminal
path: no spinner frames, no color. The check is `isTerminal(r.out)` against
**stdout**, though frames are written to **stderr** — so `kx get pods 2>/dev/null`
on a real terminal still suppresses the spinner while keeping the table.

## Isolating state

kx keeps its index history in `~/.kx/state.json` and its settings in
`~/.kx/config.toml`. Point `KX_STATE` and `KX_CONFIG` somewhere else to drive
it without touching your own:

```bash
go build -o /tmp/kx ./cmd/kx

tmp=$(mktemp -d)
KX_STATE=$tmp/state.json KX_CONFIG=$tmp/config.toml /tmp/kx get pods -n diagnostics
KX_STATE=$tmp/state.json KX_CONFIG=$tmp/config.toml /tmp/kx describe 1
rm -rf "$tmp"
```

Don't isolate by overriding `HOME`. It also hides kubectl's config (kubectl
falls back to `localhost:8080`) and the discovery cache that kx reads CRD
shorthands from, so kinds look un-normalized. Under `go run` it also relocates
the module cache.

This is also how to compare two builds on the same cluster without their state
files fighting: one `KX_STATE` per binary, both built up front.

## Test cluster

Any cluster your kubeconfig reaches works; a local one (kind, docker-desktop,
minikube) is the safe choice. The flows below assume a `diagnostics` namespace
of intentionally broken workloads (ImagePullBackOff, CrashLoopBackOff, OOM,
Pending, not-ready) — ideal for status colors, events, and `kx diagnostic`.
`web-healthy` is the healthy Deployment; its pods are safe to delete (the
ReplicaSet recreates them).

It comes from `demo/seed/`, so it can be rebuilt rather than repaired:

```bash
demo/seed.sh            # apply
demo/seed.sh --delete   # tear down
```

## Scenarios

Situations that cannot be reached by applying manifests and waiting — history
with a specific age, a status no controller would write — live in
`tests/scenarios/`, one namespace each:

```bash
go run ./tools/scenario list
go run ./tools/scenario apply stale-history
go run ./tools/scenario delete stale-history
```

`stale-history` is the one to reach for when verifying `kx diag --since`: it
carries failures 46 days, 21 days and 10 minutes old, so the same namespace
answers differently at three window widths. See `tests/README.md` before
writing another — a pod on a real node cannot hold a backdated status, and
the reason is worth reading once.

## Flows worth driving

- `kx get pods` → indexed table, status colors, spinner
- `kx diag` → namespace triage table; `kx diag <index>` → the detailed report
- `kx tree` → namespace ownership forest, indexed by default, so the numbers
  shown are usable by later commands (`--no-index` skips saving)
- `kx logs <deploy-index> --tail=1` → selector-lookup spinner, then streaming
- `kx delete <pod-index>` fed `printf 'y\n'` → confirm prompt must render
  *before* any spinner; a prompt underneath a repainting status line cannot be
  read
- `kx describe 99` → styled error path (✗ in the error color)
- `kx ns <index>` → **mutates your kubeconfig namespace — switch back to
  `diagnostics` after** (`kx get namespaces` then `kx ns <its index>`)

## Exit codes

kx forwards kubectl's exit code rather than reporting success over a failure, so
they are worth asserting on:

```bash
kx describe 1 -- --bogus-flag; echo $?   # non-zero — kubectl failed
kx exec <pod-index> -- sh -c 'exit 42'; echo $?   # 42, the container's own
```

A command that prints an error and exits 0 is a bug, not a cosmetic issue.
A flag kx doesn't know is refused by kx itself (exit 1) before kubectl runs;
put it after `--` to reach kubectl.

## Gotchas

- zsh expands bare `=word` — quote separators like `"===LOGS==="` in
  compound commands.
- The pty from `script` inherits the current terminal width; long resource
  names truncate with `…`. That is the flex-column fitting in
  `internal/render/table.go`, not a bug.
- `go test` never reaches the spinner: `Status` starts nothing off-terminal.
  Tests drive the unexported `status()` with the terminal check and timings
  injected — see `internal/render/status_test.go`.
- Local coverage can disagree with CI if the toolchain module in your module
  cache is incomplete. `go: no such tool "covdata"` during `go test
  -coverprofile` means packages with no test files are being dropped from the
  profile rather than counted as 0%, inflating the total.
