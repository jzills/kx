---
name: kx-release
description: Use when preparing a kx release, choosing its version number, cutting and pushing the release branch, or checking a release after it publishes. For writing release-notes/vX.Y.Z.md itself, use kx-release-notes.
---

# Preparing a kx release

`RELEASING.md` is the procedure. This skill names the steps agents get wrong.

## 1. Pick the number from the rule

Read RELEASING.md's "Pick the number" before proposing one.

- Features ship in **patch** releases.
- Bump the **minor** when an existing command's behavior changes. Any merged PR title carrying `!` means a minor.
- List the PR titles since the last tag and say which rule applies: `git log --oneline v<last>..origin/develop`.
- Run the gate (`gofmt -l`, `go vet ./...`, `go test -race ./...`) on develop as it stands after the last merge. Each PR's CI ran against the develop it branched from, so two green PRs can still fail together.

## 2. Write the summary

Write `release-notes/vX.Y.Z.md` with the `kx-release-notes` skill: a short lead, a few bullets, and a line on what to do only when something breaks or needs a step.

Commit it as `docs(release): summarize vX.Y.Z`. A docs type becomes no bullet, so the summary doesn't list itself.

## 3. Fact-check every claim against a built binary

Do this before pushing. Claims written from memory of the PRs have been false before.

```bash
git checkout release/vX.Y.Z
go build -o /tmp/kx-new ./cmd/kx
git worktree add /tmp/kx-prev v<last> && go -C /tmp/kx-prev build -o /tmp/kx-old ./cmd/kx
```

For each sentence in the summary, run the command it describes on `/tmp/kx-new`, and on `/tmp/kx-old` for anything it calls changed or fixed. Use isolated state from the `kx-verify` skill. Rewrite or drop any sentence the binaries don't bear out.

A note with no closing "what to change" line claims nothing breaks, and that is a claim too. Check it against every merged fix: a command that now succeeds where it used to fail has changed its exit code, so "only wording changed" is false even when every change is a fix. v0.9.2's draft said exactly that, and the fact-check caught it.

Before you call something a regression, check whether the previous tag already behaved that way. If it did, file it as an issue rather than fixing it inside the release.

## 4. Preview, then push

```bash
go run ./tools/gen-release-notes --version X.Y.Z
```

Check the preview. Breaking changes should match the `!` titles, Fixes, Features and Dependencies should hold what you expect, and no paragraph should be wrapped. Then push `release/vX.Y.Z`. The workflow pauses for approval before anything is published.

## 5. After it publishes

Download the published archive, verify it against `SHA256SUMS`, and rerun the summary's claims against that binary rather than the branch build. Each archive is attached twice, as `kx_vX.Y.Z_<os>_<arch>.tar.gz` and a version-free `kx_<os>_<arch>.tar.gz`, so name one when extracting: a glob matches both, and `tar xzf` reads the second as a member to extract.

Then check the rest of what the run produced:

- PyPI: the package is `kx-cli`. Use the version-specific endpoint, `https://pypi.org/pypi/kx-cli/X.Y.Z/json`; the bare `/json` is cached.
- The `Release vX.Y.Z` PR to `main` and the merge-back PR to `develop` both merged.
- The `Krew` workflow run, dispatched once the release publishes, finished green.
