---
title: Quickstart
description: One listing, then five minutes of doing things to it by number.
weight: 2
---

## List something

```bash
kx get pods
```

{{< kx-terminal >}}

That is kubectl's own table with a column in front of it. The numbers are the
whole idea: they are what every other command takes.

## Use the numbers

```bash
kx describe 3
kx logs 2 -f --tail=100
kx yaml 1
kx exec 2
```

The index is resolved against the listing you just ran, so `3` means the
third row of it — `cache-oom-cc849dbdb-2qdkw` above — until you list
something else.

Several at once, and ranges:

```bash
kx describe 1 3 5     # three resources
kx delete 3..5        # a range
kx delete ..3         # from the start
kx delete 5..         # to the end of the listing
```

## Drop the `get`

Known kinds can be spelled directly, kubectl's shorthands included:

```bash
kx pods
kx deploy -n kube-system
kx svc -m api
```

An integer after a kind relists just that row — `kx po 3`. Anything `kx`
doesn't recognise as a kind falls back to `kx get <resource>`, so a CRD you
have installed works the same way.

## Narrow the listing

`--match`/`-m` filters rows by name substring, and any flag `kx` doesn't
recognise goes through to kubectl untouched:

```bash
kx pods -m api
kx pods -n prod -l app=web
kx pods -A
```

`-A` is indexed like everything else: each row remembers the namespace it
came from, so `kx logs 7` reaches into a namespace you aren't in.

## See where you are

```bash
kx state
```

That prints the listing your indexes currently resolve against, and the
namespace and context it was read in. `kx` keeps the last ten listings — see
[state and history](../../concepts/state/) for moving between them.

## Where next

- [Indexes and selection](../../concepts/indexes/) — everything the numbers
  can do
- [State and history](../../concepts/state/) — what an index resolves
  against, and when it stops
- [Configuration](../../concepts/configuration/) — `~/.kx/config.toml` and
  the environment overrides
- [Themes](../../concepts/themes/) — including the one this page is drawn in
