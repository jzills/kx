---
title: Take a node out of service
description: Diagnose a node, stop the scheduler using it, and evict what is already there.
weight: 7
---

Between listing nodes and reaching for kubectl there used to be nothing. These
work off the same index [`kx get nodes`](../../reference/commands/get/) and
[`kx top nodes`](../../reference/commands/top/) hand out.

```bash
kx get nodes
kx diag 1        # what is wrong with it
kx cordon 1      # stop scheduling new pods here
kx drain 1       # and evict the ones already running
kx uncordon 1    # put it back
```

## Diagnosing a node

`kx diag <index>` on a Node reports its conditions, whether it is cordoned,
and a tally of what is scheduled on it.

The Ready condition is tri-state, and the two bad states are worded apart.
`False` is the kubelet saying the node is not ready. `Unknown` is the kubelet
not saying anything — the node may be running everything on it perfectly
behind a kubelet that has stopped talking, and calling that "not ready" would
assert something kx cannot see. Both are critical. MemoryPressure,
DiskPressure, PIDPressure and NetworkUnavailable are inverted relative to
Ready, and `True` on any of them is critical too.

Cordoned is a warning rather than a critical: it is usually deliberate, and it
is exactly what `kx cordon` just did.

The pods are a tally rather than a table — a real node runs hundreds, and a
table that long is not a diagnosis. It counts the pods that are stuck:
pending, or in a phase the kubelet has not reported. Pods that have finished,
successfully or not, are left out. Kubernetes keeps a terminated pod's object
on the node until garbage collection, so counting those would leave a node
reporting a problem long after the pod that caused it stopped mattering. A pod
that failed is a fact about the workload that owns it, and `kx diag` on that
workload reports it.

## Cordon takes several, drain takes one

```bash
kx cordon 1 3
kx cordon 1..3
kx uncordon 1..3
```

Cordon and uncordon take several indexes and ranges, like
[`kx delete`](../../reference/commands/delete/), and validate the whole batch
before acting on any of it — if one index in the range is not a Node, nothing
is cordoned.

`kx drain` takes one index, deliberately. It evicts running workloads and
blocks until they are gone, so applying it to a range in one command is a way
to take a cluster down by typo.

```bash
kx drain 1
kx drain 1 --yes                  # skip the confirmation prompt
kx drain 1 --ignore-daemonsets --delete-emptydir-data
```

It prompts before doing anything unless you pass `--yes`, and streams
kubectl's own progress, which can run for minutes. kubectl's drain flags pass
through: `--force`, `--grace-period`, `--ignore-daemonsets`,
`--delete-emptydir-data`, `--timeout`.

## Nodes are not in a namespace

A Node is cluster-scoped, so its listing records no namespace and its captions
do not name one — `Nodes · 1 item`, not `Nodes · prod · 1 item`. The same is
true of PersistentVolumes, StorageClasses, ClusterRoles, CRDs and Namespaces
themselves. Nodes also stay out of a namespace sweep and out of `-A`; you
reach one by index, from `kx get nodes` or `kx top nodes`.
