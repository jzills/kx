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

## Usage as a signal, not just state

Findings also draw on live resource usage, the same data
[`kx top`](../../reference/commands/top/) reports. A pod running hot against
its memory limit is flagged as an OOMKill risk *before* it gets killed, which
is the one finding you cannot get from `describe`.

## Wider than one namespace

```bash
kx diag -n prod    # a namespace you aren't in
kx diag -A         # every namespace
```

`-A` indexes the sweep too, and adds a `NAMESPACE` column beside the numbers,
so `kx logs 7` reaches whichever namespace row 7 came from.

## In a browser

```bash
kx diag --html
```

renders the same sweep as a filterable, sortable page and opens it — with a
group-by for larger sweeps, and each row expanding into that resource's full
report. See [browser reports](../browser-reports/).

{{< kx-shot report="diag" alt="kx diag --html dashboard" >}}
