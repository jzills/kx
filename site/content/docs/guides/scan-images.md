---
title: Scan images for CVEs
description: kx scan resolves a workload's unique images and scans each one, with Docker Scout, Trivy or Grype.
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
Because that is the scanner's own formatting rather than something kx parses,
`--full` cannot be combined with `--json`, `--html` or `--fail-on`; kx refuses
those pairings rather than quietly dropping one side.

## Engines

`kx scan` drives an external scanner, and needs its CLI installed.

| Engine | Flag | CLI |
| --- | --- | --- |
| Docker Scout (default) | `--engine scout` | <https://docs.docker.com/scout/> |
| Trivy | `--engine trivy` | <https://trivy.dev/> |
| Grype | `--engine grype` | <https://github.com/anchore/grype> |

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

Images are scanned two at a time. The bound is memory rather than cores — a
scanner unpacks an image and walks every package in it — so a wide sweep stays
steady on a small machine instead of thrashing it. Rows come back in the order
the images were resolved, whatever order the scans finish in.

## As a check

```bash
kx scan -n prod --json
kx scan -n prod --fail-on high
```

`--json` prints the severity counts and every finding as a document, and
`--fail-on` exits 2 when any image carries a vulnerability at that severity or
worse. See [using kx in CI](../use-kx-in-ci/).

## In a browser

```bash
kx scan --html
```

Per-image severity counts up top, and a CVE table below grouped by image, each
row expanding into its full detail — which is more than the terminal summary
has room for, and costs no extra scanner run: the data was already gathered to
build the table.

{{< kx-shot report="scan" alt="kx scan --html dashboard" >}}
