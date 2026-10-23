# kx style guide

This is the house voice for everything kx says: errors, captions, help text, the README and site docs, code comments, commits, pull requests and release notes.
The rules a machine can check are pinned by tests (named beside them) and by `tools/voicecheck`, which pre-commit runs over the whole tree; the rest is judgment, and this page is where that judgment is written down.

When something you are writing has no rule here, find the closest existing message and match its shape exactly: its punctuation, casing and field order.
A new shape that merely looks similar is drift.

## The voice

Plain, concrete and specific.
Say what happened, then what to do about it.
Explain *why* when the reason is not obvious, because a reader who knows the reason can handle the case the rule did not foresee.

- kx is named in the third person: "kx refuses…", "kx keeps its index history in…".
- Docs address the reader as "you", never "we".
- American spelling everywhere, in strings, docs and comments alike: color, behavior, recognize, canceled, labeled.
- An em dash ( — ) joins a statement to its consequence or its fix. It is used freely and consistently, with a space on each side.
- No label prefixes: no `note:`, `warning:`, `info:`, `error:`, `kx:` or `<command>:` in front of a message. The glyph, or the muted style, already says what kind of message it is.

### Kubernetes names

- A resource kind is written as the type, capitalized, the way the Kubernetes docs write API objects: "a Pod", "7 Deployments", "one of its Pods", "evicts the Pods on a Node". It is never lowercase as an ordinary word, so a reader can always tell the type from the English.
- What the user types keeps its own spelling, in quotes: `'kx get pods'`, `'pods' (the default) or 'nodes'`.
- Three words stay lowercase. "namespace" stays lowercase when it means the scope a resource lives in ("in the current namespace"), and is capitalized only for Namespace objects ("List Namespaces"). "container" stays lowercase because it is not an API object. "event" stays lowercase because events are read as a stream ("No Warning events in prod"). A node of the ownership graph is a "tree node".
- Condition and status values take the API's spelling, unquoted, since quotes mean the user typed it: "its Available condition is True", "a Pending Pod", "CrashLoopBackOff".
- None of these are styled. Casing carries the distinction, so it survives `--no-color`.

`TestHelpWritesKindsAsTypes` checks help text. Messages are held to it by review.

## Errors

Errors go to stderr after a ✗ (`render.Error`). Every one is a sentence or two:

- It opens with a capital letter, a quoted value, or the command it is about (`kx unmark requires…`).
- It ends with a period, or with a question mark when it offers a suggestion.
- Anything the user types is in single quotes: flags, values, commands, names. The quotes are load-bearing, because `emphasizeQuoted` accents what sits between them.
- It names the problem, then the fix, usually after an em dash.

```
✗ A mark needs a name — try 'kx mark api 3'.
✗ '--key' requires '--decode' — add it, or drop '--key'.
✗ 'gett' isn't a kx command — did you mean 'get' or 'set'?
✗ Invalid value for '--since': '7x' is not a duration — use a number and a unit (s, m, h or d): 90s, 30m, 12h, 7d.
✗ 'theme' in ~/.kx/config.toml must be a string.
```

Shapes that recur, and must stay one shape each:

| Situation | Shape |
|---|---|
| A malformed value | `Invalid value for '<flag>': '<value>' is not a valid <type>.` |
| A value outside a fixed set | `Invalid value for '<flag>': '<value>'. Accepted values: <a>, <b>.` |
| A missing argument | `kx <command> requires <args> — see 'kx <command> --help' for usage.` |
| An argument too many | `'<arg>' isn't an argument of kx <command> — see 'kx <command> --help' for usage.` |
| An unknown flag | `'<flag>' isn't a flag of kx <command> — see 'kx <command> --help' for usage.` |
| A flag with no value | `'<flag>' needs a value.` |
| Two flags that conflict | `'<a>' cannot be combined with '<b>' — <why>.` |
| An unsupported value | `kx <command> does not support '<x>' — only <list>.` |
| An unknown name | `Unknown theme '<name>'. Run 'kx theme' to list themes.` |

Cobra's and pflag's own refusals are reworded into these shapes in `internal/cli/voice.go`; nothing cobra writes should reach the terminal unchanged.

An error that exists only to be wrapped by another (`config.ParseDuration`'s, say) follows Go's convention instead: a lowercase fragment with no closing period.
The sentence that wraps it supplies the capital and the period: `Invalid value for '--since': %w.`

Exit codes are part of the message: 1 means kx failed, 2 means kx worked and found something (`--fail-on`), and kubectl's own code passes through.
A command that prints an error and exits 0 is a bug.
Return `SilentError` once a failure has already been reported, so it is not reported twice.

## Other output

| Kind | Renderer | Shape | Example |
|---|---|---|---|
| Success | `render.Success` (✓, stdout) | A past-tense statement, no closing period | `✓ Theme set to 'nord'` |
| Notice | `render.Notice` (muted, stderr) | A sentence, with a period | `Run 'kx get pods' to refresh the list.` |
| Caption | `render.Caption` (muted) | A lowercase fragment, parts joined by ` · `, no period | `watches can't be indexed — showing a live view; press Ctrl-C to stop` |
| Empty state | `render.Caption` | Capitalized, names the next step, no period | `No marks set — run 'kx mark <name> <index>' to create one` |
| Spinner | `render.Status` | A lowercase gerund, no punctuation | `fetching events` |
| Listing header | the listing renderer | `{Kind} · {namespace} · N items` | `Pods · diagnostics · 14 items` |

- A namespace-wide sweep opens with a scope banner (`{kinds} · {namespace} · N items`), and the blocks under it leave out what the banner already says.
- Don't add counts that restate what is already on screen; a count earns its place when it tells the reader whether to scroll.
- On a document command (`exec`, `debug`, `yaml`, `logs`), everything kx adds goes to stderr, so stdout stays the document.

## Help text

- **`Short`** is one imperative sentence ending in a period: `Show full kubectl describe output for one or more indexed resources.` Anything extra follows a semicolon: `…; alias: kx diag.`
- **`Long`** is in the third-person present ("Shows…", "Graphs…", "Blocks until…"), in paragraphs separated by `\n\n`. It explains what the command does and why a refusal exists. Once a paragraph backticks one flag, it backticks all of them (`TestLongFormatsFlagsOneWay`).
- **`Example`** is a list of two-space-indented `kx …` lines with no prose and no `$`; the renderer adds the prompt. Every output-shaping flag the command has (`--json`, `--html`, `--out`, `--fail-on`) appears in at least one example.
- Every argument named in `Use` has text in `argDocs` or an `arg.<name>` annotation.
- A new command goes in a `helpSections` section, alphabetically. That order is also the README table's and the site's.
- When syntax changes, update the `Example` strings as well as the README; they lag otherwise.
- Write "completion script" and "subcommand", not "autocompletion" or "sub-command" (`TestCompletionHelpUsesKxsOwnVoice`).

The README command table and the site's command pages are generated from these strings, so changing help text means rerunning `go run ./tools/gen-command-table` and `go run ./tools/gen-site-docs`.

## README and site docs

- One sentence per line, with no hard wrap. A list item continues on the next line with a two-space indent.
- No sentence opens with inline code. Name what the code is ("The `--html` flag…"), or reword ("With `--key`, …").
- No sentence opens with a lowercase "kx" either. Reword so kx is not first.
- kx the product is plain text. Code formatting is for things you type.
- Code blocks are 80 columns or narrower, because GitHub never wraps them.
- The README is a pitch and the site is the reference. The README has no sample-output blocks or worked examples; GIFs and short prose carry the features, and the generated command table is the reference.
- Images go in `<p align="center"><img … width="800"/></p>`. The banner stays in a `<div>`.
- A section ends its prose with "See the [<topic> guide](url) for more." It is always "guide".
- README links are absolute, because the README is also the PyPI description.

## Code comments

- Comments are full sentences, and a doc comment opens with the name of what it documents.
- They say *why*: the trap being avoided, the alternative that was rejected, the history that explains a shape. They don't restate *what* the code says.
- Match the density of the file you are in. Most of kx's files are heavily commented, and a new function with no comment stands out.
- A test's doc comment names the regression it guards against. If you can't name one, the test may not be able to fail.

```go
// findingsExitCode is what --fail-on exits with when a report breaches its
// threshold.
//
// Two, not one. kx exits 1 for its own failures, so a pipeline that reused it
// could not tell "the cluster is sick" — which is the check working — from
// "kx could not reach the cluster", which is the check not having run.
```

## Commits and pull requests

Titles are Conventional Commits, and the release notes are generated from them (`tools/internal/relnotes`):

- `type(scope): subject`, lowercase, with no closing period. The scope names the command or area (`yaml`, `cli`, `state`), and several scopes are comma-joined.
- The subject states the new behavior rather than the act of changing it: `fix(yaml): the separator precedes the banner it comes before`.
- `feat` becomes a Features bullet, `fix` and `perf` become Fixes bullets, and `chore(deps)`/`build(deps)` become Dependencies bullets. `docs`, `style`, `test`, `refactor`, `ci` and plain `chore` appear only in the commit list.
- A `!` goes on the **pull request title**, before it merges, whenever an existing command's behavior changes: `fix(state)!: …`. It leads the release's Breaking changes section, and marking it after the merge is too late.
- Branches are `fix/…`, `feat/…`, `docs/…`, `chore/…`, `test/…` or `style/…`, then a kebab-case description.

A commit body explains the change to someone reading `git log` a year from now:

1. what was wrong, concretely, with a before/after block when the output is the point
2. why the tests didn't catch it, when they didn't
3. what the fix does, and anything it deliberately leaves alone

A pull request description has the same shape: what changed, what was left out on purpose, and a **Testing** section saying what was run and how each new test was shown to fail without the fix.
Neither carries attribution trailers or session links.

## Release notes

`release-notes/vX.Y.Z.md` is the Highlights section of a release, and you write only that part; see [RELEASING.md](RELEASING.md) for the rest.

- Write one line per paragraph, never hard-wrapped. GitHub turns every newline in a release body into a line break.
- No headings.
- Write in the second person, for someone using kx, with commands in backticks and concrete before-and-after examples.
- The last paragraph says what upgrading asks of the reader, even when the answer is nothing.
