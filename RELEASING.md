# Releasing kx

A release is two gestures: push a branch named `release/vX.Y.Z`, then approve
the run once the build comes back green. Everything else — tests, six
binaries, eight wheels, PyPI, the tag, the GitHub release, the krew-index
submission, and the pull requests back to `main` and `develop` — is
[`.github/workflows/release.yml`](.github/workflows/release.yml).

There is nothing to run locally and nothing to publish by hand. Nothing is
published — nothing is even committed — until you approve.

## Before you push

1. **`develop` is green.** The workflow runs `go test ./...`, but a clean
   commit is not a passing one — the pre-commit hooks never run the tests:

   ```bash
   go build ./...
   gofmt -l ./cmd ./internal ./tools   # must print nothing
   go vet ./...
   go test -race ./...
   go run ./tools/gen-command-table    # README table must be current
   go run ./tools/gen-site-docs        # site command reference must be current
   ```

2. **Pick the number.** kx ships features in patch releases — v0.3.2 brought
   open-ended index ranges, v0.3.3 brought shell completion. Bump the minor
   when a release changes what an existing command *does*, not merely when it
   adds something.

3. **Write the summary.** `release-notes/vX.Y.Z.md` is a short paragraph or
   two, in your own words, that opens the release notes. See below.

4. **Branch and push:**

   ```bash
   git checkout develop && git pull
   git checkout -b release/v0.5.3
   # write release-notes/v0.5.3.md, commit it
   git push -u origin release/v0.5.3
   ```

## Writing the summary

Release notes have three tiers, and you write only the first:

| tier | comes from |
|---|---|
| `## Highlights` | `release-notes/vX.Y.Z.md` — you |
| `## Breaking changes` / `## Features` / `## Fixes` / `## Dependencies` | derived from what merged |
| `## What's Changed` | GitHub, unchanged |

They are siblings at the same heading level, which is the one GitHub's own
generated block already uses.

The bullets are derived rather than written so they cannot advertise something
that never shipped. The paragraph is written rather than derived because no
tool can say why a release matters.

Say what changed for someone using kx, and whether upgrading asks anything of
them. [`release-notes/v0.5.2.md`](release-notes/v0.5.2.md) is the worked
example — it names the one thing that needed a reinstall.

A release that breaks something says so twice. The `## Breaking changes`
section is derived, from the conventional-commit `!` on a pull request title —
`fix(state)!: drop the top-level back, forward and drop aliases` — so marking
the title is what puts it there, and marking it after the merge is too late.
The bullet names the change; the paragraph is where you say what to do about
it. Bump the minor for one, per "Pick the number" above.

**One line per paragraph — do not hard-wrap it.** GitHub renders a release
body with line breaks on, so every newline inside a paragraph becomes a
literal `<br>` and the text breaks at whatever column it was typed to,
however wide the reader's window is. The generator copies the paragraph
verbatim and will not reflow it: reflowing would be a tool editing prose it
did not author, and it would join lines a summary broke on purpose. v0.6.0
shipped wrapped at 76 columns and had to be edited after the fact — the
worked example had never been rendered, because the three tiers post-date
v0.5.2's own release.

Preview it before pushing:

```bash
go run ./tools/gen-release-notes --version 0.5.3
```

Only `feat`, `fix`, `perf` and dependency bumps become bullets. Everything
else — docs, style, test, refactor, plain chores — appears in the commit list
below. That is deliberate: the block exists to be scanned. A `!` overrides
that: any type carrying it leads the block instead, since a chore that raises
the minimum Go version costs its readers something a feature often does not.
Each change is listed once, in the first section that claims it.

**The summary is required.** The workflow checks for it before it installs Go,
and fails the run if it is missing or empty. That check is first because it is
the one thing the build cannot tell you about itself, and a five-minute build
is a wasteful way to find out the paragraph is missing.

## What the pipeline does

Six jobs. The order is load-bearing.

| job | does |
|---|---|
| **Build** | checks the summary, runs the tests, cross-compiles, verifies the archives, builds and installs a wheel, uploads the archives and the wheels |
| **Publish to PyPI** | *waits for your approval*, then stamps `pyproject.toml`, pushes the bump commit, uploads the wheels |
| **Create Release Tag** | tags `vX.Y.Z` and pushes it |
| **Publish GitHub Release** | checksums, assembles the notes, creates the release, validates the krew manifest, dispatches krew-index |
| **Open PR to Main** | opens and auto-merges the release PR |
| **Merge Release Back to Develop** | opens the merge-back PR |

**Build** writes nothing outside the runner. Everything that does not come
back cleanly — the bump commit, PyPI, the tag, the release, the krew
submission, both pull requests — is downstream of the approval, so a run
abandoned at the gate leaves the release branch exactly as you pushed it.

Binaries are built before wheels because the wheels package them. The krew
manifest is validated *after* publishing because the validator installs from
real URLs with real checksums, so it cannot run earlier — what it buys is
finding out within a minute rather than from a stuck pull request on someone
else's repository.

## Approving the release

**Build** takes about five minutes. When it finishes, the run page shows
*Review deployments* — tick `release`, then **Approve and deploy**. Nothing
downstream starts until you do.

The point of the pause is to look at what the build proved before it becomes
permanent. In the **Build** job's log:

- *Check the bundled version matches the release* — the binary reports the
  number you are releasing.
- *Verify the archives are krew-installable* and *Verify the Windows
  archives* — six archives, each with its binary and its `LICENSE`.
- *Install a wheel and run the installed binary* — the last line is the
  installed `kx --version`.

Then decide. **Approve** and the rest of the pipeline runs unattended.
**Reject**, or simply cancel the run, and nothing was published, nothing was
tagged, and no commit was pushed: delete the branch and the run stops
mattering. A run left pending expires after 30 days.

The `release` environment holds the required-reviewer rule and is restricted
to `release/*` branches. `prevent_self_review` is deliberately **off** — with
it on, the only reviewer could never approve their own run and every release
would deadlock.

## After it finishes

The workflow reporting success is not the same as the release being right.
Check the artifacts themselves:

```bash
git fetch --tags && git tag -l v0.5.3

# six archives plus SHA256SUMS
gh release view v0.5.3 --json assets --jq '.assets|length'    # expect 7

curl -s https://pypi.org/pypi/kx-cli/json | jq -r .info.version    # expect 0.5.3
gh run list --workflow=krew.yml --limit 1                          # expect success

# the strongest check: run what was actually published
curl -sL -o kx.tgz https://github.com/jzills/kx/releases/download/v0.5.3/kx_v0.5.3_linux_amd64.tar.gz
tar xzf kx.tgz && ./kx/kx --version && test -f kx/LICENSE && echo "LICENSE ok"
```

## Why the pipeline is shaped the way it is

Each of these is a scar.

- **`RELEASE_PAT`, not `GITHUB_TOKEN`.** The `restrict-release` ruleset
  protects `release/**` and bypasses a specific user, not the Actions bot —
  integration bypass actors need an org-owned repository. The version-bump
  commit, pushed from the publish job, uses a fine-grained PAT issued under
  that user.

- **`[skip ci]` on the bump commit.** GitHub suppresses on-push retriggering
  only for the built-in token. A PAT push to `release/**` would start a second
  run of this same workflow, which fails at the bump step because the version
  is already bumped.

- **`kx/LICENSE` inside every archive.** krew-index refuses a plugin whose
  archive carries no license. The PyInstaller bundles satisfied this by
  accident, so v0.1.0 was the first release to trip it — and it tripped at
  krew-index, as a stuck external pull request nobody was watching.

- **`fetch-depth: 0` on the release job.** The notes are built from the tag
  list and the commit range between the last two releases. A shallow clone has
  neither and produces empty notes rather than an error.

- **Artifacts are staged in `RUNNER_TEMP`, not the workspace.** `assets/` is a
  real directory here — the banner and the screenshots the README embeds — and
  downloading the archives into it merged the two, so the upload's glob shipped
  those five images with every release from v0.0.6 to v0.5.2. A step now checks
  that the staging directory holds nothing but archives and `SHA256SUMS`.

- **The archive download names its artifact.** The run carries the wheels as
  well as the archives, and `download-artifact` with `merge-multiple: true`
  and no `name:` takes every artifact in the run — which would stage eight
  wheels next to the release assets and trip the check above.

- **The tag is pushed with `GITHUB_TOKEN`,** which never triggers downstream
  workflows — so the krew update is an explicit `workflow_dispatch` rather
  than `on: push: tags`.

## When something fails

**Before the PyPI publish** — the summary check, the tests, a build or
archive check, or an approval you decided not to give. Nothing was published,
no tag exists, and the release branch carries no bump commit. Fix it, commit
to the release branch, and push: that re-runs the workflow from the top.

**After the PyPI publish.** A version cannot be re-uploaded to PyPI. Do not
retry the same number — cut the next patch instead. The `--skip-existing` flag
means a re-run will not error on the already-published wheels, but the release
you get is the one assembled by the later run.

**The krew validation fails after the release is published.** The GitHub
release and PyPI are fine; only the krew-index submission is affected. Fix
`.krew.yaml` and re-dispatch:

```bash
gh workflow run krew.yml --ref v0.5.3
```

**A release branch that should not have been pushed.** If the run is still
waiting for approval, reject it and delete the branch — the remote never
changed. If it was approved but has not tagged yet, delete the branch
(`git push origin --delete release/v0.5.3`) and the run stops mattering. Once
the tag exists, the release exists — go forward, not back.
