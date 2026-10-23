---
name: kx-tests
description: Use when writing, changing or reviewing a Go test in kx, when claiming a test covers a fix, or when about to run the suite and commit.
---

# Tests in kx

A kx test only counts once you have watched it fail on the regression it names. Several tests in this repo passed while the bug they were named for was present: the assertion was satisfied by something else.

## Write the test

- **Use real argv.** Drive commands through `executeArgv(cmd, argv)` or `Execute(NewRoot(argvServices(t), "test"), argv)`. Calling `cmd.RunE(cmd, []string{"--flag", …})` skips cobra's parse and reads the flags as positionals.
- **Pin the whole message.** Compare `err.Error()` to the full sentence when the wording is the point, so a regression fails by name.
- **Give every negative assertion a control.** "X is not printed" passes when the fixture can't produce X at all. Run the same fixture down the path that *does* print X, and assert that it does.
- **Anchor negative assertions to syntax,** such as a quoted attribute, a closing tag or the full message, never a bare token. An inlined stylesheet or a library's error text will contain it.
- **Prove a lookup found something** before asserting on what it found. A zero value passes most checks.
- **Watch for the fixture trap.** Saving a second history entry with the *same query* replaces the first instead of pushing, so the cursor stays at 0. Give the seeded entry its own query, and assert `Cursor >= 1`.
- **Write the doc comment** to name the regression the test guards against.

## Prove it can fail

1. Name the broken implementation the test should catch.
2. Make exactly that change, and only that change. Delete the guard rather than the whole function, because a wider revert hides what you meant to test.
3. Run the one test and watch it fail on the assertion you meant, not on setup.
4. Restore the code, then confirm with `git diff` that nothing of the mutation is left.

In zsh, an unquoted `$FILES` is not split into words. List the files inline or use an array, and check that the backup exists before you mutate.

## Run the gate, then commit separately

The gate and the commit are **separate commands**. Read the gate's result before you issue the commit.

```bash
gofmt -l ./cmd ./internal ./tools    # must print nothing
go vet ./...
go test -race ./...                  # read the result: ok or FAIL
```

Only after the last line reads `ok` everywhere, as a new command:

```bash
git commit
```

A pipe reports its last command's status, and so does a script without `set -e`. So all of these commit over a red suite:

- `go test ./... | tail -1 && git commit`
- `go test ./... ; git commit`
- a script with `go test` on one line and `git commit` on a later one

Pre-commit runs gofmt, vet and the generators, but not the tests.
