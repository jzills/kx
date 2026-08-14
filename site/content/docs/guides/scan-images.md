---
title: Scan images for CVEs
description: kx scan resolves a workload's unique images and scans each one, with Docker Scout or Trivy.
weight: 2
---

Scanning a workload means knowing which images it actually runs — including
init containers, and the pod template inside a CronJob. `kx scan` works that
out from the resource and scans each unique image once.

```bash
kx scan 1        # the images of indexed row 1
kx scan          # every workload in the namespace
```

The default output is a severity summary table: one row per image, counts by
severity.

## The full report

```bash
kx scan 1 --full
```

streams the scanner's own output instead — the per-image CVE list, unabridged.

## Engines

`kx scan` drives an external scanner, and needs its CLI installed.

| Engine | Flag | CLI |
| --- | --- | --- |
| Docker Scout (default) | `--engine scout` | <https://docs.docker.com/scout/> |
| Trivy | `--engine trivy` | <https://trivy.dev/> |

```bash
kx engine          # list engines and show the current default
kx engine trivy    # persist a new default
```

The default is stored in [`~/.kx/config.toml`](../../concepts/configuration/);
`--engine` overrides it for one run.

## Wider than one namespace

```bash
kx scan -n prod
kx scan -A
```

## In a browser

```bash
kx scan --html
```

Per-image severity counts up top, and a CVE table below grouped by image, each
row expanding into its full detail — which is more than the terminal summary
has room for, and costs no extra scanner run: the data was already gathered to
build the table.

{{< kx-shot report="scan" alt="kx scan --html dashboard" >}}
