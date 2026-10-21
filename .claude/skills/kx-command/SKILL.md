---
name: kx-command
description: Use when adding a kx command or subcommand, changing one's arguments, flags, Use string or annotations, or making a command forward to a kubectl subcommand it didn't before.
---

# Adding or changing a kx command

A command touches more than its own file. Pinning tests walk the whole tree, several lists are hand-kept, and the README table and site pages are generated from the command's help strings. Work through this list in order. CLAUDE.md's Architecture section explains the why behind each item.

## The command

1. **Constructor.** Write `newXCommand(services Services) *cobra.Command`, following `newScaleCommand` in `internal/cli/commands.go`. Its dependencies come from `Services`, never from package globals.
2. **`Use`.** `ParseUse` reads it, and help, completion and the missing-argument errors are built from it. Name every argument, and end a forwarding command with `[kubectl flags]`, or with `[-- command...]` when it takes a command to run.
3. **Argument docs.** Add every new argument name to `argDocs` (`help.go`), or give it an `arg.<name>` annotation. `TestEveryArgumentIsDocumented` checks this.
4. **Completers.** Add every new argument name to `argCompleters` (`completion.go`), keyed `<cmd>.<arg>` when it differs per command. Map it to `nil` when there is nothing to guess. Never set `ValidArgs`.
5. **Help text.** Write a `Short`, a `Long` of the command's own, and an `Example`, in the `STYLE.md` Help text shapes. `TestEveryCommandHasItsOwnLongDescription` checks the `Long`. Any of kx's own output flags the command has (`--json`, `--html`, `--out`, `--fail-on`) must appear in `Example` (`TestOutputShapingFlagsAppearInExamples`). kubectl's flags don't need to.
6. **`helpSections`** (`help.go`). Add the name to its section, alphabetically. That list also orders the README and the site.

## Flags and kubectl

7. **A command that forwards to kubectl.** Register the flags kx reads itself first. Then call `registerKubectlFlags(cmd, "<kubectl subcommand>")` and keep the `*kubectlArgs` it returns.
   - A new kubectl subcommand needs adding to `subcommands` in `tools/gen-kubectl-flags/main.go`, and then `go -C tools/gen-kubectl-flags run .`. Until then `registerKubectlFlags` panics.
   - Mark with `ownFlag` any kubectl-named flag that kx builds into the argv itself.
   - Split the argv with `kubectlArgv`, `indexArgv` or `splitIndexArgv`. A mutating command refuses leftover positionals with `refuseStray`.
   - Never set `DisableFlagParsing`, and never give a forwarding command an `Args` validator.
8. **Errors.** Use the `STYLE.md` Errors shapes, through the `kx-voice` skill.

## Wiring and annotations

9. **`root.go`.** An index-consuming command joins the `withRefresh` loop. Anything else is added with `withoutRefresh`, with a comment saying why.
10. **Annotations.** Never write into these maps in place; they are shared.
    - If the command changes the cluster, use `mutatingAnnotations`.
    - If its stdout is always a document, use `documentAnnotations`.
    - If it is both, use `mutatingDocumentAnnotations`.
11. **Mutating.** Call `installAgentIndexNotice(services)` from `RunE`, on the path that mutates.

## Tests and hand-kept lists

12. **Pinning lists.** Add the command to each of these lists. Each fails until the command is on one side or the other:
    - the mutating or read-only map in `TestAgentIndexNoticeCommandAllowlistIsPinned`
    - the document or other map in `TestDocumentAnnotationAllowlistIsPinned`, by full path for a subcommand
    - `mutatingCommandCases()` if mutating, or `TestAgentIndexNoticeReadOnlyCommandsSilent` if it reads an index
    - the name list in `TestEveryCommandNamesItsMissingArgumentsTheSameWay`
13. **Behavior tests.** Drive real argv through `executeArgv` (or `Execute(NewRoot(argvServices(t), "test"), argv)`), never `cmd.RunE` with flags in the args. Use the fakes `fakeKubectl`, `recordingKubectl` and `scriptedKubectl`. Follow the `kx-tests` skill.

## Generated files: regenerate, never hand-edit

14. Each of these is generated. Change the source and rerun the generator:

    | Generated file | Source | Generator |
    |---|---|---|
    | README command table | help strings | `go run ./tools/gen-command-table` |
    | `site/content/docs/reference/commands/*.md` | help strings | `go run ./tools/gen-site-docs` |
    | `internal/kubectlflags` | the `subcommands` list | `go -C tools/gen-kubectl-flags run .` |
    | `internal/cli/testdata/mcp-tools.golden.json` | MCP tool schemas and descriptions | `KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas` |

15. **Release notes.** Nothing to write. The PR title's type and `!` produce the bullet; see the `kx-pr` skill.

## Before you finish

Run `gofmt -l ./cmd ./internal ./tools`, `go vet ./...` and `go test -race ./...`. Then drive the command against a cluster with the `verify` skill, including its `--help` and `kx __complete <cmd> ""`.
