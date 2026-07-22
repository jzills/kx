# Demo recordings

The GIFs embedded in the top-level README are rendered from the
[VHS](https://github.com/charmbracelet/vhs) tapes in this directory:

| Tape | GIF | Shows |
| --- | --- | --- |
| `demo.tape` | `demo.gif` | Hero demo — get, top, logs, triage, drill-in, tree |
| `diag.tape` | `diag.gif` | Namespace triage and per-resource diagnosis |
| `theme.tape` | `theme.gif` | Theme listing with previews, switching themes |

## Prerequisites

- `vhs` on your PATH
- a local cluster reachable via kubectl, seeded with the demo namespace (below)
- kubectl's current namespace set to `diagnostics` (`kx ns` → pick it)
- the project virtualenv at `.venv/` (`render.sh` activates it and pins the
  theme to `github-dark` for the recording, restoring your theme after)

## Seed the demo namespace

```bash
demo/seed.sh            # apply demo/seed/, wait for failure states to settle
demo/seed.sh --delete   # tear down
```

`demo/seed/` creates a `diagnostics` namespace full of intentionally broken
workloads — CrashLoopBackOff, ImagePullBackOff, OOMKilled, unschedulable,
never-Ready, a failing Job and CronJob, an endpointless Service, and a
Pending PVC — plus a healthy control Deployment (`web-healthy`) and a pod
near its memory limit (`memory-pressure`). Give the namespace a few minutes
after seeding so restart counts and events accumulate.

## Render

```bash
demo/render.sh          # render every tape
demo/render.sh diag     # render just demo/diag.tape
```

Each tape's `Output` directive names its GIF. Re-record after feature work
that changes command output, then commit the updated GIFs — the README embeds
them via raw.githubusercontent URLs pinned to `main`, so new GIFs only render
on GitHub once merged.
