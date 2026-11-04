---
name: kx-release-notes
description: Use when writing or revising release-notes/vX.Y.Z.md, the Highlights section that opens a kx release on GitHub.
---

# Writing kx release notes

`release-notes/vX.Y.Z.md` is the **Highlights** section, the first thing anyone opening the release reads. The generator adds Breaking changes, Features, Fixes and Dependencies from the PR titles, then GitHub's New Contributors and Full Changelog link. Highlights only has to answer two questions: what's new for me, and do I have to change anything?

Someone scanning a release page reads the opening and the bullets, and skips anything longer. v0.10.0 through v0.12.0 ran 250–470 words of paragraphs: why the bug existed, how the flag interacts with three others, every command it reaches. That detail belongs in the PRs and the docs.

## The shape

```markdown
<Lead: two or three sentences on what this release is about.>

- **<What you notice, in a few words>:** <One complete sentence, with one example.>
- **<…>:** <…>.

<Only when something breaks or needs a step: what to do, in a sentence or two.>
```

- **Lead:** two or three sentences, under about 60 words. Say what the release does for someone using kx, not how it's built. A one-fix patch can be the lead alone.
- **Bullets:** one per change a user would notice, at most six. Each is a bold label of a few words, a colon, then one complete sentence with one example in backticks: a command, or the exact line kx prints. Pick the example that best shows the change; never list every command it reaches.
- **Label and sentence both open with a capital:** "**Resources by name:**", not "**kind/name targets:**". The sentence after the colon opens the way any kx sentence does, never with inline code or a lowercase name, and ends with a period: "The `--context` flag sends…", not "`--context` sends…".
- **What to do:** a closing line only when something breaks, or when upgrading needs a step: reinstalling completions, adding a symlink. For a break, name the invocation that changed and what a script should do instead, as in "If a script reads …, read … now". Every `!` PR title needs its fix spelled out here, since the derived Breaking section only names it. When nothing breaks, leave the line out: "Upgrading asks nothing of you" is filler, and no closing line already says it.
- **Length:** about 150 words in all. Over 200 means something belongs in the docs instead.

## Leave out

- **Mechanism and history:** why the bug existed, which library behaved oddly, what an earlier release did. Name the new behavior and stop.
- **Flag combinations:** how a flag behaves beside `--json`, `--html` and `--fail-on`. Mention one only when it is the change.
- **Anything the derived sections already say:** a bullet that restates one PR title adds nothing. Fold small fixes into one bullet, or leave them to the Fixes list.
- **Internal names:** functions, files, field names. A JSON field a script reads is fine.

## Format rules (`tools/voicecheck` enforces them)

- Each paragraph, and each bullet, is a single line. GitHub turns every newline inside one into a `<br>`. Bullets are fine, since each `- ` line starts its own item.
- No headings: the generator supplies `## Highlights`.
- Never open a sentence with inline code or a lowercase "kx". In a bullet, the sentence after the label is the one checked.
- Every bullet has a capitalized bold label, then a sentence that opens with a capital and ends with a period.
- Second person, and American spelling.

## Worked examples

A minor release (`release-notes/v0.12.0.md`):

```markdown
Every command that takes an index now also takes a resource by name, the way kubectl writes it, so reaching one you already know no longer means listing and counting rows. A new k9s plugin pack runs kx's reports on the row under k9s's cursor.

- **Resources by name:** Commands that take an index also take kind/name, so `kx diag deploy/api` and `kx logs pod/web-7d -f` work wherever `3` or `@api` did.
- **Other clusters:** The `--context` flag sends a kind/name to another cluster, and a prompt names it first: `Delete Deployment/api in default on context 'prod'?`.
- **Typos reported, not relisted:** A kind/name that names nothing prints `Deployment/typo was not found in namespace 'default'.` and leaves your listing alone.
- **Marks by name:** A mark can pin a resource without a listing, as in `kx mark api deploy/api`.
- **Plugins for k9s:** Five keys run `kx diag`, `tree`, `scan`, `events` and `mark` from inside k9s ([guide](https://jzills.github.io/kx/docs/guides/use-kx-from-k9s/)).
```

A one-fix patch (`release-notes/v0.11.1.md`):

```markdown
Pressing TAB after a descending sort key, as in `kx get pods --sort -re<TAB>`, now completes it, on every command that takes `--sort`. It used to complete nothing.
```

A patch that breaks a script (`release-notes/v0.9.1.md`):

```markdown
Output kx passes through from kubectl is now kubectl's bytes exactly, and several manifests from `kx yaml` join the way their format does.

- **No trailing blank line:** The output of `kx get pods -o json` now matches `kubectl get pods -o json` byte for byte.
- **Valid JSON for several manifests:** Piping `kx yaml 1 2 -o json` to `jq` reads one List instead of failing after the first object.

If a script reads `kx yaml <several indexes> -o json`, read `.items[]` now: several manifests come back as one List. One index still prints the bare object.
```

## Before you commit

1. Count the words in the file, and check the lead is three sentences or fewer.
2. Run `go run ./tools/voicecheck`, which the pre-commit hook also runs.
3. Fact-check every bullet against a built binary, as the `kx-release` skill's step 3 describes. A short note is only better if it is still true.
4. Preview with `go run ./tools/gen-release-notes --version X.Y.Z`. No bullet should repeat a derived Features or Fixes line word for word.

## The derived sections

The Features, Fixes and Dependencies bullets under Highlights are PR titles, turned into sentences by the generator, which capitalizes the subject's first letter and adds a period. A title is only release copy if its subject can take that capital, so it opens with a word: "a plugin pack that runs kx on…", never "--context reaches…" or "kx tree is…". The `kx-pr` skill holds the title rules.
