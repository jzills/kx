# Cluster scenarios

Fixtures that put a cluster into a situation `kx` is supposed to diagnose, so
a behaviour can be seen rather than described.

```bash
go run ./tools/scenario list
go run ./tools/scenario apply stale-history
go run ./tools/scenario delete stale-history
```

Each scenario owns a namespace (`kx-<name>`), applied and deleted whole, so
scenarios never disturb each other or the demo namespace.

## Not the base cluster

The everyday broken workloads — CrashLoopBackOff, ImagePullBackOff, a pending
PVC, a Service with no endpoints — live in [`demo/seed/`](../demo/README.md)
and are applied with `demo/seed.sh`. They are the base state most manual
verification runs against, and nothing here duplicates them.

A scenario earns its own directory when the situation cannot be reached by
applying manifests and waiting: history with a specific age, a status no
controller would write, a shape that needs its own namespace.

## Writing one

```
tests/scenarios/<name>/
  README.md       first line "# <name> — <one-line summary>"; `scenario list` reads it
  manifest.yaml   applied with kubectl apply
  status.yaml     optional: status patches, applied after the objects exist
```

**Two files, because `kubectl apply` drops a status block.** Anything a
controller would normally write — a container's termination, a Job's failure —
has to be patched onto the object afterwards, which is what `status.yaml` is.

**Timestamps are relative.** `"@-46d"` becomes an RFC 3339 time 46 days before
the moment you applied it, in both files. A committed fixture with an absolute
date ages into meaninglessness, and the offsets are read by the same parser as
`kx diag --since`, so `46d` means the same thing in a fixture and in the flag
that filters it.

**Ages have to be faked deliberately.** A pod on a real node has its status
rewritten by the kubelet within seconds; a pod bound to a node that does not
exist keeps whatever you patch into it. See `stale-history` for the pattern.

## No assertions

These deploy a situation; they do not check the output. Running the commands
and reading what comes back is the point — an expected-output file would fix
the wording of a report that is still being shaped, and the behaviour itself
is covered by `go test ./...`.
