# stale-history — failures old enough that `--since` should hide them

Three resources whose failures are 46 days, 21 days and 10 minutes old, so one
namespace answers "does the window work?" at three different widths.

Nothing here can be produced by waiting: a fixture applied today is minutes
old, and the ages are the point. See [how it fakes them](#how-the-ages-are-faked).

## Run it

```bash
go run ./tools/scenario apply stale-history
```

```bash
kx diag -n kx-stale-history                # unbounded — all three critical
kx diag -n kx-stale-history --since 24h    # only recent-crash survives
kx diag -n kx-stale-history --since 1m     # all healthy
```

```
Mixed · kx-stale-history · 3 checked
  1    Job     ancient-run      critical    BackoffLimitExceeded (2/1 failed)
  2    Pod     ancient-crash    critical    OOMKilled in pod ancient-crash
  3    Pod     recent-crash     critical    Container app in pod recent-crash terminated…

Mixed · kx-stale-history · 3 checked · last 24h
  1    Pod     recent-crash     critical    Container app in pod recent-crash terminated…

Mixed · kx-stale-history · 3 checked · all healthy · last 1m
```

`kx diag <index>` on `recent-crash` shows the other half — a live failure and
stale noise on one resource:

```
  ✗ Container app in pod recent-crash terminated: Error (exit 1) · 10m ago
  ✗ Pod recent-crash failed · 10m ago
  ! FailedScheduling ×3 on Pod/recent-crash · 21d ago     ← gone under --since 24h
```

```bash
go run ./tools/scenario delete stale-history
```

## What it proves

| Fixture | Age | Exercises |
| --- | --- | --- |
| `ancient-crash` | 46d | terminal state and a previous OOMKill, both dated; a restart count dated by the termination that ended it |
| `ancient-run` | 46d | a Job's failed run, dated by its Failed condition |
| `recent-crash` | 10m | the control — proves a narrow window empties the report rather than the report being empty |
| `stale-failed-scheduling` | 21d | a warning event outliving the pod's own failure |

## How the ages are faked

Both pods are bound to `node-that-does-not-exist`. Nothing schedules them, so
no kubelet ever writes their status — which is what lets `status.yaml` give
them terminations weeks in the past. On a real node the kubelet rewrites a
patched status within seconds, so aged container history cannot be faked
there.

`ancient-run` is suspended so the Job controller creates no pods for it; its
failure is written rather than earned, which is the only way to have one older
than the fixture.

The event needs no trick at all — an Event is an object you write, timestamps
included.
