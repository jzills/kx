---
title: Indexes and selection
description: Numbering, ranges, kind shorthand, matching, and -A listings.
weight: 1
---

Every `kx get` numbers the rows of the listing it prints, starting at 1, in
the order kubectl returned them. Those numbers are what the rest of `kx`
takes.

```bash
kx get pods
kx describe 2
```

The numbering is not a naming scheme and it is not stable across listings —
it is a handle on *the listing on your screen*. List something else and the
numbers mean something else. [State and history](../state/) covers exactly
what "the listing on your screen" resolves to.

## Several at once

Most commands are variadic. Space-separated indexes are taken in the order
you gave them:

```bash
kx describe 1 3 5
kx events 2 4
kx yaml 1 2
```

## Ranges

A run of consecutive indexes can be written as a range instead of listed out.
Ranges walk in either direction, so `5..2` is as valid as `2..5` and does the
work in that order.

```bash
kx delete 3..7
```

Either end can be left open, and the open end means the listing's own end:

```bash
kx describe ..3      # rows 1, 2, 3
kx describe 5..      # row 5 to the last row
```

Ranges and single indexes mix freely — `kx delete 1 2..4 7..` is one command.

## Dropping the `get`

A first word that names a kind is treated as `kx get <kind>`:

```bash
kx pods
kx deploy -n kube-system
kx svc -m api
```

kubectl's own shorthands work — `po`, `deploy`, `svc`, `sts`, `ds`, `cm` and
the rest. So do CRDs: their short name, kind, or plural, resolved from
kubectl's on-disk API-discovery cache, so `kx` never calls the API server just
to decide whether a word is a kind.

Registered commands always win. `kx ns 3` switches namespace rather than
listing namespaces, because `ns` is a command; only a spelling that matches no
command reaches the kind shorthand. A spelling that is neither a command nor a
known kind still falls through to `kx get <resource>`, so kubectl gets the
last word on whether it exists.

An integer after a kind relists just that row:

```bash
kx po 3
```

## Filtering

`--match`/`-m` filters rows by case-insensitive name substring, after kubectl
has returned them:

```bash
kx pods -m api
```

Everything `kx` doesn't recognise is passed through to kubectl untouched —
label selectors, field selectors, output formats, `-n`:

```bash
kx pods -n prod -l app=web
kx pods --field-selector status.phase=Running
```

{{% kx-note %}}
`-o json` and friends pass through too, but there is no table to number in
that output, so nothing is indexed. The command prints what kubectl printed.
{{% /kx-note %}}

## Resources with no namespace

Some kinds do not live in one — Nodes, PersistentVolumes, StorageClasses,
ClusterRoles, CRDs, and Namespaces themselves. Their listings record no
namespace, and the caption leaves the segment out rather than naming whichever
one you happened to be standing in:

```text
Nodes · 1 item
```

not `Nodes · prod · 1 item`. The commands that resolve those indexes need
nothing extra — `kubectl` ignores `-n` for a cluster-scoped resource — but a
scope printed back at you should be one the resource actually has.

Kinds kx cannot place — a CRD missing from the cluster's discovery data —
keep a namespace rather than having one stripped on a guess. Being wrong about
a caption is cheaper than resolving every index into the wrong place.

## Every namespace

`-A` is indexed like any other listing. Each row records the namespace it came
from, so an index reaches a resource in a namespace you aren't currently in,
and two pods sharing a name in different namespaces each keep their own
number.

```bash
kx get pods -A
kx logs 7            # reaches whichever namespace row 7 came from
```

The listing gains a `NAMESPACE` column beside the numbers so you can see which
is which.

## Watching

`--watch`/`-w` redraws the table live as resources are added, changed and
removed, instead of printing one that is out of date the moment it lands:

```bash
kx get pods --watch
```

It is display-only. A watch never completes, so there is no final listing to
number — nothing is indexed and the saved state is left as it was. With a
non-tabular output format, `kx` streams kubectl's own watch output straight
through rather than trying to redraw it.

## When an index has gone stale

Resources get deleted. When a command fails because the resource behind an
index is gone, `kx` re-runs the query that produced the listing and prints a
fresh one, so there are usable numbers on the screen rather than an error and
a dead end. The failure is reported first — the refresh is the recovery, not a
retry of what you asked for.
