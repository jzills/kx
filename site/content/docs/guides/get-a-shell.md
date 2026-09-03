---
title: Get a shell in a workload
description: kx exec takes a Deployment as readily as a Pod, so you don't have to list pods first.
weight: 6
---

Running a command inside something usually means finding a pod first: list
them, read a generated name off the table, then type it. `kx exec` takes the
index of the workload itself.

```bash
kx get deploy
kx exec 1              # a shell in one of that Deployment's pods
kx exec 1 -- ps aux    # a command instead
```

With no command it tries each configured shell in turn — `bash`, then `sh`,
unless the [`shells`](../../concepts/configuration/) key says otherwise. That
matters for distroless and Alpine images, where the shell you assume is not
the one that's there.

## What it accepts

Pods, Deployments, ReplicaSets, StatefulSets and DaemonSets.

Services are refused. `kubectl exec` does not accept one, and kx says so
itself rather than letting kubectl produce a worse message about it.

{{% kx-note %}}
kx does not pick the pod — `kubectl exec` resolves the workload, the same way
[`kx port-forward`](../../reference/commands/port-forward/) leaves that choice
to kubectl. Which pod you get is therefore not guaranteed to be the same one
across the shell probe and the session that follows. For a Deployment whose
replicas are interchangeable that is what you want; when it isn't, address a
pod directly.
{{% /kx-note %}}

## Exit codes come back

The command's own exit code is forwarded, so `kx exec` works inside a script:

```bash
kx exec 1 -- test -f /etc/config.yaml || echo "config missing"
```

kubectl's own stderr is suppressed here, because a failing command inside a
container otherwise prints "command terminated with exit code N" on top of
whatever the command already said.
