# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Environment

Go (version pinned by the `go` directive in `go.mod`). No other toolchain is required.

```bash
go build ./...
```

## Running the CLI

```bash
go run ./cmd/kx --help
go run ./cmd/kx <command> [args]
```

## Checks

```bash
gofmt -l ./cmd ./internal ./tools   # must print nothing
go vet ./...
go test -race ./...
```

`pre-commit run --all-files` runs the first two, plus the README command-table generator. It does **not** run the tests — a clean commit is not a passing one, so run `go test -race ./...` yourself.

## Stack

- **cobra/pflag** — CLI framework; the command tree is built in `internal/cli/root.go` with injected dependencies
- **client-go** — used in `internal/graph`, `internal/events`, `internal/diagnostics` for the structured data kubectl's table output can't provide
- **lipgloss/termenv** — color profile and style rendering behind `internal/render`
- **BurntSushi/toml**, **yaml.v3** — config parsing and `kx yaml --show` re-serialization

## Architecture

`kx` is a kubectl wrapper adding index-based resource selection. The workflow: `kx get <resource>` lists resources and saves state; all other commands resolve a numeric index back to a resource name from that saved state.

**State flow:** `kx get` → `kubectl get` output → `index.Service.Add()` parses the NAME column and assigns 1-based indexes → a `state.State` (an ordered `Resources` mapping of name→kind, plus `Namespace` and the `Query` that produced it) is persisted to `~/.kx/state.json` via `state.Service.Save()`. State is a history stack with a cursor (up to `MaxHistory` entries); subsequent commands call `state.Service.Fields(index)`, which loads the current entry and resolves the index to `(name, namespace, kind)`. Every method that writes the file takes an exclusive lock on `<path>.lock` first, load and save inside the same hold, so the CLI and a long-lived `kx mcp` server never race a read-modify-write into a lost update. A lock file that can't be opened for writing is opened read-only and locked that way; where no lock can be taken at all (a filesystem without locking, or a lock file that won't open even read-only) the write runs unlocked, as kx did before the lock, rather than failing.

`state.Resources` is an ordered slice with hand-written `MarshalJSON`/`UnmarshalJSON`, **not** a Go map. Order is load-bearing — an index resolves to the nth entry — and map iteration is randomized, so a map here would resolve indexes to different resources on different runs. The on-disk JSON shape is a compatibility surface: existing installs have `~/.kx/state.json` files that must keep resolving the same way.

**Command pattern:** Commands are structs in `internal/cli/`, constructed in `root.go` with interface-typed dependencies (`kubectl.Service`, `IndexResolver`, `StateWriter`, `Indexer`) plus plain callables where useful (`render.Confirm` for `delete`, `render.Status` for spinners). Tests substitute fakes without subprocess or filesystem side-effects. `withRefresh` wraps the index-consuming commands so a failure caused by a vanished resource re-runs the saved query and renders a fresh listing; it returns `SilentError` because it has already reported the failure itself.

**Flag pass-through:** Commands that forward flags to kubectl set `DisableFlagParsing` and split argv by hand via the helpers in `internal/cli/passthrough.go`. Cobra has no equivalent of Typer's `ignore_unknown_options` — `FParseErrWhitelist` *discards* unknown flags rather than passing them through. Flags parsed by hand must still be **registered** on the command, or they work but vanish from `--help`. Cobra's arity check runs against the unstripped argv, so a command whose arguments are all kx's own flags reaches its index lookup with an empty slice — guard for it.

**Two kubectl wrappers** (`kubectl.Service` in `internal/kubectl`):
- `Run(args)` — captures stdout; returns kubectl's stderr as the error on a non-zero exit (used for `get`, `yaml`, `delete`, `scale`, `rollout`)
- `RunInteractive(args, quietStderr)` — streams stdio through to the terminal, returns the exit code (used for `describe`, `logs`, `exec`, `edit`, `port-forward`)

  Plus `Probe(args)` (silent, returns an exit code — used by `exec` shell detection and staleness checks) and `CurrentNamespace()`/`CurrentContext()`.

**client-go usage** (`internal/k8s`): `Client()` loads the caller's kubeconfig, falling back to in-cluster config. The `tree` and `diagnostic` commands walk ownership references through the API rather than kubectl, because ownership isn't in kubectl's table output.

**Theming:** `internal/theme` defines a registry of prefab palettes as semantic style names (`header`, `muted`, `body`, `status.ok`, …). All styling flows through the package-level renderer in `internal/render` (`Configure(theme, plain)` swaps it); render code names styles by meaning, never by hex. Theme selection: `theme` key in `~/.kx/config.toml`, `KX_THEME` env, or `kx theme <name>` (persisted via `config.Loader.SaveTheme`).

**Table layout** (`internal/render/table.go`) is reimplemented rather than delegated to a table library, because column widths, padding and alignment are pinned by test. `internal/index` owns the one parser (`ParseTable`) and formatter (`Format`) for kubectl's table output — don't add a second copy.

**`kinds.Normalize()`** in `internal/kinds` maps kubectl shorthand (e.g. `pods`, `deploy`, `svc`) to canonical Kubernetes kind names, used for kind checks and event filtering.

**MCP server** (`kx mcp`, `internal/cli/mcp*.go`): a Model Context Protocol server for AI agents. It lives in package `cli`, not its own — a split was considered and deliberately deferred — because its handlers reuse `cli`'s unexported document builders and the command structs above (`DiagnosticCommand`, `TreeCommand`, `TopCommand`, `ScanCommand`, …) directly, while `cli` is what registers `kx mcp`; a separate package would import `cli` and be imported by it. A target can also carry an `index` — a row number from the user's current kx listing, resolved through the same `state.Service` path `kx describe 3` takes, then validated exactly like a typed target (`validateResolved`: flag shapes, the `all` refusal, core-group spellings reduced and normalised), as a mark's is, before it ever reaches argv; the index itself is read-only and is never echoed back. Each listing handler reads the context once, at call start before any client or kubectl call, labels its output with it, and takes its save hook from `mcpDeps.listingSave(context)`/`listingWriter(context)` rather than a hard-coded discard: off (the default, `kx mcp` run plainly), they return `discardListing`/a discarding `StateWriter` — `top` and the `diagnose` sweep still call that Save, while `list_resources` and `tree` skip it, `tree` via `indexed=false`, which exists so no index appears in the output; single-target `diagnose` (`DiagnosticCommand`) has no save path to cut off in the first place. Started with `--write-listings`, `listingSave` instead returns the real `d.State.Save` with the entry's `Source` set to `state.SourceMCP` and its `Context` preset to the call-start one (`stamp` leaves a preset Context alone, so a context switch mid-sweep cannot file one cluster's rows under another), so `list_resources`, the `diagnose` sweep, `tree` (a single-target `tree` call included — it still saves the walk it returns) and `top` save their listing as the matching CLI command would — `list_resources` on namespaces refreshes the ns slot as `kx get ns` does, and it refuses kx's `Context` pseudo-kind, whose save would overwrite `kx ctx`'s slot — and return each row's index, tagged so `kx state` and the destructive CLI confirms can say the listing came from an agent; with the flag on those four tools also drop `readOnlyHint` (`listingTool`). Non-negotiables: read-only against the cluster (kubectl `get`/`logs`/`top` only, client-go `get`/`list`/`watch` only — `RunInteractive` and `Watch` are never called); every target string, typed, marked or resolved from an index, reaches argv only through `resolveTarget`'s validation; with the flag off, no tool writes anything but a mark, and no index appears in any output; a mark write still refuses any name that is already a mark. Every handler but `scan` runs `serialized`, which takes `mcpDeps.mu` (`locked`) for the call's whole duration — kx was built as a CLI that does one thing at a time, and concurrent tool calls would otherwise race the state file and client-go's process-wide error handlers; `scan` holds the lock only while resolving images, since a sweep can run for minutes, and gates its own scanner phase through `scanSlots` (capacity 1) so two concurrent scans never double the scanner memory budget. `internal/cli/testdata/mcp-tools.golden.json` pins each tool's schema and description, plus the server instructions — the public surface an agent sees — and is regenerated with `KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas`. A mutating CLI command that resolves an index into an agent-tagged (`Source: SourceMCP`) listing prints a muted stderr notice before it acts. Each command arms it itself, by calling `installAgentIndexNotice` (`internal/cli/agent_notice.go`) from its own RunE; the `mutatingAnnotations` annotation arms nothing and only feeds the pinning test that makes every command pick a side. `delete`/`drain` carry the annotation but arm the notice only with `--yes` — without it, their confirm prompt names the provenance instead — and `rollout` arms it only for `restart`/`undo`/`pause`/`resume`. The notice is a lock-free read of the current entry; `mark`'s atomic "refuse an existing name" check (`SaveMarkIfAbsent`) and every other state write go through the `<path>.lock` file described above, so a `kx mcp --write-listings` server and a terminal session never lose one write to the other's.

## README command table

The command reference table in `README.md` is generated from the command tree by `tools/gen-command-table`, between the `<!-- commands-table-start -->` sentinels. Adding or changing a command means regenerating it — the pre-commit hook does this and fails if the table was stale. Command order comes from `helpSections` in `internal/cli/help.go`, which is also the root help screen's order.

## Release

Releases are triggered by pushing a `release/vX.Y.Z` branch. One runner cross-compiles six targets (linux/darwin/windows × amd64/arm64), packages them as tar.gz — plus zip for Windows — builds eight PyPI wheels around those binaries, publishes to PyPI, creates the GitHub Release, and dispatches the krew-index update.

The published wheels carry a static binary and declare no dependencies; `pyproject.toml` exists only to supply their name/description/readme and to record the released version. See `scripts/build_binaries.sh` and `scripts/build_wheels.py`.
