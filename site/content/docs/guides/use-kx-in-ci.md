---
title: Use kx in CI
description: --json prints the analysis as a document, --fail-on turns it into a gate that exits 2.
weight: 8
---

`kx diag` and `kx scan` both sweep a namespace and print a table. In a
pipeline nothing reads the table, and both would exit 0 whatever they found.
Two flags change that. (`kx tree` and `kx top` take `--json` too, for the
ownership graph and the usage listing; only the two that produce findings take
`--fail-on`.)

```bash
kx diag -A --fail-on critical    # 0 if the cluster is healthy, 2 if not
kx scan -n prod --fail-on high --json
```

## `--json`

The same analysis as a machine-readable document — every resource swept,
healthy ones included, and every CVE behind the severity counts.

```bash
kx diag -n prod --json | jq '.resources[] | select(.verdict == "critical") | .name'
kx scan -n prod --json | jq '.images[] | select(.counts.critical > 0)'
```

It is built from the same values the terminal and `--html` render, so the
three views cannot disagree about what is wrong. A sweep serialises every
resource regardless of `--full`: that flag governs how much of a table fits on
a screen, and nothing is scrolling past a machine.

The document carries a `schemaVersion`, because this is a public surface the
moment it ships — something will parse it in a pipeline, and a field moving
underneath that is worse than one it can check for.

```json
{
  "schemaVersion": 1,
  "namespace": "prod",
  "checked": 12,
  "healthy": 1,
  "resources": [ … ]
}
```

All four commands that emit `--json` name their subject with the same fields,
so a pipeline that reads one does not have to learn a second shape to read the
others. A sweep
carries `namespace`, or `allNamespaces: true` for `-A`; an indexed run carries
`kind`, `name` and `namespace`:

```json
{
  "schemaVersion": 1,
  "kind": "Deployment",
  "name": "api",
  "namespace": "prod",
  "images": [ … ]
}
```

`kx tree --json` names every node with `kind` and `name` rather than the
`rs/web-7d8f` label the terminal draws, carries the same `index` the tree
printed, and always returns a `roots` list — one entry for an indexed resource
or a single namespace, one per namespace for `-A`. A pod's containers appear
as children with a name and no kind, because a container is part of a pod
rather than a resource of its own.

`kx top --json` reports percentages as numbers, and as `null` where there is
none — a pod with no limit set has no percentage, and `0` would read as idle.
Its `resource` field says whether the listing was pods or nodes, since a pod's
percentage is against its limits and a node's against its capacity.

Severities are lower case throughout — `critical`, `high`, `medium`, `low` for
image findings, `critical`, `warning`, `healthy` for verdicts — which is
exactly what `--fail-on` accepts, so a value read out of a document can be
typed straight back at the gate.

## `--fail-on`

Turns either command into a gate.

| Command | Accepted thresholds |
| --- | --- |
| `kx diag` | `critical`, `warning` |
| `kx scan` | `critical`, `high`, `medium`, `low` |

The threshold is inclusive: `--fail-on high` fails on high *and* critical. It
is validated before the cluster is read, so a typo fails immediately rather
than after a sweep has already run.

An image whose scan failed breaches every threshold, for the same reason a
missing test is not a passing one: an image kx could not read has not been
shown to be clean.

## The exit code is 2

Two, not one, and the difference is the point:

| Code | Meaning |
| --- | --- |
| `0` | The check ran and found nothing at the threshold. |
| `1` | kx itself failed — no cluster, no scanner, bad flags. The check did **not** run. |
| `2` | The check ran and found something. |

A pipeline that treated any non-zero as "unhealthy" could not tell a sick
cluster from an unreachable one.

## Publishing a report and failing on it

`--fail-on` is independent of how the findings are presented. It applies
alongside `--json` and [`--html`](../browser-reports/) alike, so a job can
publish a report and still fail on what is in it:

```bash
kx diag -A --fail-on critical --html --out diag.html
```

`--out` writes the page and returns, so the gate runs and the file is there
for an artifact step to pick up. Plain `--html` serves the report and blocks
until Ctrl-C, which is right at a terminal and wrong in a pipeline — nothing
sends Ctrl-C to a CI job, so it would hang until the runner killed it.

{{% kx-note kind="warn" %}}
`kx scan --full --fail-on` is refused rather than ignored. `--full` streams
the scanner's own report, which kx never parses, so the gate would have
nothing to read. `--json` and `--full` are refused together for the same
reason, and `kx diag --json --full` is refused because a document already
carries every resource swept — `--full` has nothing to add to one.
{{% /kx-note %}}

## A whole cluster

```bash
kx diag -A --fail-on critical
kx scan -A --fail-on high
```

`kx scan -A` resolves every unique image in the cluster and scans each once,
two at a time. The bound is memory rather than cores — a scanner unpacks an
image and walks every package in it — so a wide sweep is steady rather than
fast, and does not thrash a small runner.
