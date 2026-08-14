---
title: See what owns what
description: kx tree walks ownership references from controllers down to containers.
weight: 4
---

Ownership is not in kubectl's table output. A Deployment owns a ReplicaSet
owns Pods, and finding that chain by hand means reading
`metadata.ownerReferences` out of JSON.

```bash
kx tree
```

graphs every workload in the current namespace, from controllers down to
containers. `kx tree <index>` graphs one.

```bash
kx tree 1
kx tree -n prod
kx tree -A         # every namespace, as a forest
```

A Namespace index graphs that namespace — so `kx get ns` then `kx tree 3`
works.

## The nodes are indexed too

By default `kx tree` numbers the nodes it draws and saves them as the current
listing, so the tree is a way to *select* things and not only to look at them:

```bash
kx tree          # 1  api (Deployment)
                 # 2   └─ api-7d8f (ReplicaSet)
                 # 3       └─ api-7d8f-p4k2 (Pod)
kx logs 3
```

With `-A`, nodes are indexed continuously across the whole forest.

`--no-index` skips the numbering and leaves your existing listing alone —
useful when you want to look at the structure without losing the indexes you
were working with.

## In a browser

```bash
kx tree --html
```

renders the graph as a collapsible tree, indexed like the terminal version.

{{< kx-shot report="tree" alt="kx tree --html dashboard" >}}
