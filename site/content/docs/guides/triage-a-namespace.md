---
title: Triage a namespace
description: kx diag ranks what is unhealthy, and the rows are indexed so you can drill in.
weight: 1
---

Something is wrong in a namespace and you don't yet know what. The kubectl
version of this is four commands and a lot of scrolling: `get pods`, then
`describe` the ones that look off, then `logs`, then `get events`.

```bash
kx diag
```

sweeps the current namespace — Deployments, StatefulSets, DaemonSets, Jobs,
CronJobs, Services, PersistentVolumeClaims and Ingresses, plus pods nothing
owns — and prints what's unhealthy, worst first.

Healthy resources are left out of the terminal table. `--full` puts them back.

Each row shows one finding, so which of several equally severe findings sorts
first decides what the whole sweep reads like. Rows are ordered by severity,
and within a severity by how specific the finding is: a concrete cause — a
container state, a scheduling refusal, an exceeded limit — outranks a rollup
like "Only 0/3 replicas ready", which outranks a raw warning event. Without
that, five differently-broken Deployments all headlined the same replica
count and the actual cause sat one screen down.

## The rows are indexed

That's the part that makes it a starting point rather than a report:

```bash
kx diag       # 1  api-badimage   ImagePullBackOff
              # 2  cache-oom      CrashLoopBackOff
kx diag 1     # the full diagnosis of row 1
kx logs 2     # straight into the logs of row 2
```

## One resource

`kx diag <index>` diagnoses a single resource and prints, on one screen:

- a verdict banner
- a `SUMMARY` of findings
- a per-pod status table
- recent log tails from broken containers
- warning events

The findings it looks for include CrashLoopBackOff, image pull failures,
OOMKills, unschedulable pods, stalled rollouts, Services with no endpoints,
Pending PVCs, failed CronJob runs, and Ingresses pointing at Services that
don't exist.

## Now, not once

Only what happened in the last 24h is reported — a warning event, a restart
or OOMKill a container recovered from, a run that failed. What a resource is
doing *now* is always reported, however long it has been doing it: a container
in CrashLoopBackOff, a Pending PVC, a Service with no endpoints.

That line matters because a finding drives the verdict and the verdict drives
[`--fail-on`](../use-kx-in-ci/). Without it, one `FailedScheduling` from three
weeks ago holds a healthy workload at `warnings` forever.

```bash
kx diag --since 7d    # a week of history
kx diag --since 0     # everything, the old behaviour
```

A run that failed outside the window loses its `Most recent run:` line, not
its diagnosis: the failed pods it left behind are current state, so a CronJob
whose last run failed weeks ago still reads critical. Widen the window where
those pods have been cleaned up. `diag_max_age` in
[config.toml](../../reference/configuration/) sets your own default.

## Usage as a signal, not just state

Findings also draw on live resource usage, the same data
[`kx top`](../../reference/commands/top/) reports. A pod running hot against
its memory limit is flagged as an OOMKill risk *before* it gets killed, which
is the one finding you cannot get from `describe`.

## Nodes

A Node is diagnosed the same way, by index rather than by sweep — it is
cluster-scoped, so it appears in neither a namespace sweep nor `-A`.

```bash
kx get nodes
kx diag 1
```

See [taking a node out of service](../take-a-node-out-of-service/).

## Wider than one namespace

```bash
kx diag -n prod    # a namespace you aren't in
kx diag -A         # every namespace
```

`-A` indexes the sweep too, and adds a `NAMESPACE` column beside the numbers,
so `kx logs 7` reaches whichever namespace row 7 came from.

## As a check

```bash
kx diag -A --json
kx diag -A --fail-on critical
```

`--json` prints the same sweep as a document, and `--fail-on` exits 2 when any
resource reaches that verdict. See [using kx in CI](../use-kx-in-ci/).

## In a browser

```bash
kx diag --html
```

renders the same sweep as a filterable, sortable page and opens it — with a
group-by for larger sweeps, and each row expanding into that resource's full
report. See [browser reports](../browser-reports/).

{{< kx-shot report="diag" alt="kx diag --html dashboard" >}}
