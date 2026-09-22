---
title: Marks
description: Naming a resource so it survives the re-listing that moves every index.
weight: 3
---

An index is a position in a listing, not an identity. The moment you run
`kx get` again, every number is reassigned — row 3 is whatever sorts third
this time, not the pod you meant. That is fine for a command you type and
spend in the same breath, but it falls apart the moment something else
relists in between: a script that watches logs across a redeploy, or a pod
you want to come back to after checking something else.

A mark is the fix: a name you choose, pinned to one resource, that keeps
resolving no matter how many listings happen after it.

```bash
kx mark api 3
```

pins whatever index 3 currently resolves to under the name `api`. From then
on, `@api` spends it exactly the way an index does, anywhere `<index>` is
accepted — with three exceptions: `kx ns` and `kx context` switch a
namespace/context slot, not a Kubernetes resource, so there is nothing for a
mark to have pinned there; and `kx cp` parses its own `<index>:<path>`
argument rather than taking a bare index, so a mark has nowhere to go. All
three refuse a mark explicitly, naming the reason, rather than silently
falling back to some other behavior.

```bash
kx logs @api -f
kx exec @api -- sh
kx describe @api
```

## Listing and removing marks

Bare `kx mark` lists what's set:

```bash
kx mark
```

That listing carries no index column and saves no state — a mark is spent by
the name it was given, never by position, so numbering the list would invite
`kx mark 2` to mean something it doesn't.

```bash
kx unmark api          # remove one mark
kx unmark api web db   # remove several
kx unmark --all        # remove every mark
```

Names, and as many as you like. A name that isn't a mark refuses the whole
call, so a typo partway through a list leaves every mark in place — there is
no half-removed state to work out afterwards, and a mark can't be recovered
from a listing that no longer mentions it. The `@` the listing prints is
accepted on any of them, since copying what's on screen is the obvious way to
type one.

Marking under a name that's already taken replaces it in place; a mark is a
pointer, and moving it is the ordinary operation, not an error.

## A mark is pinned to its cluster

Every mark records the kubeconfig context it was made in, for the same reason
[a listing does](../state/): a name means nothing without the cluster it was
read from, and `web` in staging is not `web` in production. Spending a mark
from another context is refused, naming both:

```
✗ @web was marked in context 'staging'; you are in 'production'.
```

There's no relist that fixes this the way a stale index gets one: switching
context is what you'd do next, not a mistake to recover from, so `kx` just
says which context the mark is waiting for.

## What outlives a re-list

An index depends on the listing that produced it; drop that listing and the
index is gone. A mark depends on nothing but its own entry, so it survives
history that indexes don't:

```bash
kx state drop --all    # clears every listing and slot — marks are untouched
kx unmark --all        # this is what removes marks
```

That asymmetry is deliberate. `kx state drop --all` clears things that
accumulate on their own, one `kx get` at a time; a mark is something you
named on purpose, and clearing it takes a command that says so.

## A mark kx cannot read is dropped

Marks live in `~/.kx/state.json` alongside the listings, and kx reads that file
on every command. An entry it cannot make sense of — one missing the resource
name, or the kind it has to ask kubectl for — is dropped as the file loads,
and the rest of the file is used as normal. `kx mark` simply stops listing it,
and spending that name reports it as unknown:

```
✗ No mark named 'web' — run 'kx mark web <index>' to create one, or 'kx mark' to list them.
```

That is deliberate, and it is the same rule a malformed listing follows. The
alternative is worse than it sounds: a mark with no kind still has a name, so
kx would ask kubectl for a resource with no type, kubectl would answer that it
has no such type, and kx would report a resource that is running perfectly well
as one that no longer exists — blaming the cluster for a problem in a local
file.

Nothing kx writes can produce such an entry; every mark is made from a listing
that already carries both fields, and the state file is written by an atomic
rename, so an interrupted write leaves the previous file rather than half of a
new one. It is worth saying only because `KX_STATE` invites pointing kx at a
file something else maintains.
