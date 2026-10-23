---
name: kx-voice
description: Use when writing or rewording any text kx shows or ships — an error, success line, caption, empty state, spinner, notice, cobra Short/Long/Example, README or site docs prose, a Go comment, a commit message, a PR description, or release notes.
---

# kx voice

kx has one voice, written down in `STYLE.md` at the repo root. Every string you write takes a shape kx already uses.

## Recipe

For each string:

1. **Read the matching `STYLE.md` section:** Errors, Other output, Help text, README and site docs, Code comments, Commits and pull requests, or Release notes.
2. **Find the closest existing message in the code** (`grep -rn` in `internal/cli`, `internal/render`), and write yours in its exact shape: the same punctuation, casing, quoting and field order. The recurring error shapes are tabulated in `STYLE.md`. A malformed value, an unknown name, a missing argument and conflicting flags each have one sentence already.
3. **Pick the renderer by what the message is,** not by tone:
   - ✗ `render.Error` for a failure: a sentence, with a period.
   - ✓ `render.Success`: past tense, no period.
   - Muted `render.Caption` for empty states and asides: no period, and the next step after an em dash.
   - `render.Status` for a spinner: a lowercase gerund.
4. **Quote everything the user types** in single quotes: flags, values, commands.

## Quick check

| String | Correct | Not |
|---|---|---|
| Missing mark | `No mark named 'api' — run 'kx mark api <index>' to create one, or 'kx mark' to list them.` | `kx mark --rename: no mark named '@api'` |
| Value outside a set | `Invalid value for '--type': 'Info'. Accepted values: Warning, Normal.` | `'--type' must be Warning or Normal, got "Info".` |
| Nothing matched | caption `No Warning events in prod` | `✗ No Warning events found.` |

## Before you finish

- Help text changed? Run `go run ./tools/gen-command-table` and `go run ./tools/gen-site-docs`.
- An MCP tool description changed? Run `KX_UPDATE_GOLDEN=1 go test ./internal/cli -run TestMCPToolSchemas`.
- A test pins the old wording? Update the test's want to the new sentence. Don't bend the sentence back to fit the old test.
