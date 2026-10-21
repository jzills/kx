---
name: kx-pr
description: Use when naming a branch, writing a commit message, or titling and describing a pull request in kx, including deciding whether a change is breaking.
---

# Commits and pull requests in kx

The release notes are generated from PR titles, so a title is release copy. `STYLE.md`'s Commits and pull requests section holds the full rules; this is the order to apply them in.

## 1. Decide `!` first

Put `!` after the type or scope (`fix(get)!:`) when **an invocation that worked before now behaves differently**. That covers:

- what it prints to stdout
- its exit code
- what it does to the cluster or to `~/.kx/state.json`
- whether it is accepted at all

That holds even when the old behavior was a bug, and even for an opt-in flag. If a script that ran yesterday could do something different today, it is breaking.

- `!`: `-o name` no longer prints a header line, so a script that stripped it now loses a name.
- `!`: a flag kx used to accept is now refused.
- No `!`: a new flag, a new command, or a fix to stderr wording only.

The `!` must be on the **PR title** before merge, because that is what the release-notes generator reads. A breaking change also bumps the minor version (see the `kx-release` skill).

## 2. The title

`type(scope)!: subject`, lowercase, with no closing period and no backticks.

- **Type:** `feat`, `fix` or `perf` produce bullets. `docs`, `style`, `test`, `refactor`, `ci` and `chore` don't. `chore(deps)` and `build(deps)` go under Dependencies.
- **Scope:** the command or area, comma-joined for several: `fix(label,annotate)`.
- **Subject:** state the new behavior, not the edit you made. Write `fix(get): -o name prints kubectl's reply unchanged`, not `fix(get): remove header for -o name`.

One root cause goes on one branch and one PR. Name the branch `<type>/<kebab-description>`.

## 3. The commit body

Wrap at about 72 columns, and cover:

1. what was wrong, concretely, with a before/after block indented four spaces when output is the point
2. why the tests didn't catch it, if they didn't
3. what the fix does, and what it deliberately leaves alone

## 4. The PR description

- **What changed:** what the PR does, plus anything left out on purpose.
- **Breaking:** if `!`, a sentence saying what a user has to do.
- **Testing:** the checks run, and for each new test the mutation that made it fail.

Neither commits nor PR descriptions carry `Co-Authored-By`, `Claude-Session` or session links. PRs are squash-merged into `develop`.
