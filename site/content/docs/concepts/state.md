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
kx state drop --empty  # drop the entries whose listing found nothing
```

Jumping does not re-run anything: the entry already holds the listing, so the
indexes it carries resolve immediately.

Re-running the listing you are already on refreshes that entry instead of
pushing another copy of it. Re-running `kx get` is how you see what changed, so
without that the stack filled with one listing — five runs of `kx get pods`
around a single `kx get deploy` left nine entries, eight of them the same, and
`kx state back` could not reach the Deployments listing. The same session now
leaves three: pods, deployments, pods.

## Reading an index back out

`kx state` shows what every index means. `kx ref` prints one of them in a form
another command can take:

```bash
kx ref 3                          # pod/web-abc-xyz -n prod
kubectl exec $(kx ref 3) -- sh
kubectl get $(kx ref 1..3)        # one line per index
```

That is what keeps the index model from being limited to the verbs kx wraps:
anything that takes a resource — another kubectl subcommand, `stern`, `velero`,
a script of your own — can be handed one. `--name`, `--namespace` and `--kind`
print a single field for tools that want the pieces separately, and a
cluster-scoped resource comes back without `-n`, since there is no namespace for
it to be in.

`kx ref` never contacts the cluster. It reports what the index means, not what
still exists, so it answers instantly — and a stale index prints the name it was
assigned, leaving the command you spend it on to discover the resource is gone.

## A listing that found nothing is still a listing

`kx get pods -n empty-namespace` saves its result like any other listing, even
though the result is nothing. It has to: an empty listing that saved no entry
would leave the *previous* one resolving indexes, so `kx get pods -n a`
followed by `kx get pods -n b` and then `kx delete 1` deleted a pod in `a` —
a namespace and two commands away from anything on screen.

So the numbers retire when a listing finds nothing, and kx says so on the spot:

```
Pods · empty-namespace · none found
'kx state back' returns to Pods · prod · 14 items
```

Spending an index against it explains itself the same way, at the moment it
matters rather than one command earlier:

```
✗ The current listing is empty — Pods · empty-namespace found none.
  Run 'kx state back' for the previous listing.
```

Those entries cost a history slot each. `kx state drop --empty` removes all of
them at once, and needs no confirmation the way `--all` does — an entry holding
nothing is not work anyone can lose.

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
