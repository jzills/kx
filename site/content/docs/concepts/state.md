---
title: State and history
description: The stack an index resolves against, the namespace and context slots, and what a context switch retires.
weight: 2
---

An index means nothing on its own — it is a position in a listing. `kx` keeps
that listing in `~/.kx/state.json` so the next command can resolve against it.

```bash
kx state
```

That prints the listing indexes currently resolve against, along with the
namespace and the kubeconfig context it was read in.

## The history stack

Each `kx get` pushes its result onto a stack — ten entries by default, set by
[`max_history`](../configuration/) — with a cursor marking the current one.

```bash
kx state --all       # the whole stack, with positions
kx state 2           # jump to position 2
kx state back        # step back one
kx state forward     # step forward one
kx state drop 2      # remove position 2
kx state drop --all  # clear everything, slots included
```

Jumping does not re-run anything: the entry already holds the listing, so the
indexes it carries resolve immediately. The older `kx back`, `kx forward` and
`kx drop` spellings still work.

Every entry records the context it was listed in, because a resource name
means nothing without the cluster it was read from. `kx state` names it beside
the namespace; `kx state --all` captions the table with it, or gives it a
column when the history spans more than one.

## Switching contexts retires your indexes

```bash
kx get pods          # in staging
kx context 2         # switch to production
kx delete 1          # refused
```

`kx` will not resolve a staging index against production, where the same name
is a different resource. It refuses, names both contexts, and re-runs the
listing here so there are usable numbers on the screen.

`kx ns <index>` is refused the same way, and tells you to run `kx ns` — a
namespace listing is per-cluster too. `kx context <index>` is the one
exception: contexts live in kubeconfig rather than in any cluster, so
switching back always works.

## The namespace and context slots

Namespaces and contexts are kept in slots of their own, outside the history
stack.

```bash
kx ns                # list namespaces, into the namespace slot
kx ns 2              # switch to the second of those
kx contexts          # list contexts, into the context slot
```

Two things follow. An index into a slot keeps meaning the same entry however
much you have listed since — `kx ns 2` is still the second namespace you
listed, not the second row of whatever is on screen. And switching namespaces,
which is frequent, never pushes real work off the ten-entry stack.

```bash
kx state --all       # summarizes the slots under the history table
kx state --targets   # expands them into the listings the switch commands read
```

`--targets` is how you pick a number without listing again.

## Operating on a namespace, rather than switching to it

To treat a namespace as a resource — describe it, label it — list it like one:

```bash
kx get ns
kx describe 2
kx label 2 team=platform
```

That stacks it like any other listing, and refreshes the namespace slot too,
so the two spellings never disagree about what `2` means.

A narrowed listing counts, though:

```bash
kx get ns -l team=platform
kx ns 2              # the second *platform* namespace
```

Run `kx ns` to go back to indexing all of them.

## The file

`~/.kx/state.json` holds the stack, the cursor and the slots. It is versioned:
if the schema changes under an existing install, `kx` resets the file rather
than migrating it — the cost is re-running one listing, and the alternative is
migration code for a cache.

`KX_STATE` points at a different file, for a terminal or CI job that wants its
own history instead of sharing the one in `~/.kx`. `kx --version` prints the
path actually in use, in case yours is somewhere else.

```bash
# terminal A
export KX_STATE=~/.kx/state-a.json
kx get pods

# terminal B
export KX_STATE=~/.kx/state-b.json
kx get deploy
```

Each terminal now resolves indexes against its own listing — `kx delete 1` in
B never touches what A listed. [`KX_CONFIG`](../configuration/) does the same
for `~/.kx/config.toml`.
