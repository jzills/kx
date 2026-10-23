#!/bin/sh
# PreToolUse on Bash: refuse a git commit issued in the same command as the
# checks that gate it, and a commit that skips the hooks.
#
# A pipe reports its last command's status, and a script without set -e runs
# its next line whatever the last one did, so `go test ./... | tail -1 && git
# commit` and `go test ./...; git commit` both commit over a red suite. The
# gate has to run on its own, with its result read, before the commit is sent.
#
# Only the text before a heredoc is checked, so a commit message that mentions
# go test in its body is not mistaken for running it.
command -v jq >/dev/null 2>&1 || exit 0
cmd=$(jq -r '.tool_input.command // empty' | sed '/<</q')
# git's own options can sit between git and commit: git -C dir commit.
printf '%s\n' "$cmd" | grep -Eq 'git([[:space:]]+-[Cc][[:space:]]+[^[:space:]]+)*[[:space:]]+commit([[:space:]]|$)' || exit 0
case "$cmd" in
*--no-verify*)
	echo "Commit without '--no-verify' — the pre-commit hooks regenerate files CI checks." >&2
	exit 2
	;;
esac
# A check counts in command position, after any VAR=value assignments and the
# wrappers that run a command (env, time, command, nice): CGO_ENABLED=1 go test
# is go test. Anchored there so a commit message saying "go test" is not one.
if printf '%s\n' "$cmd" | grep -Eq '(^|[;&|(])[[:space:]]*(([A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*|env|time|command|nice)[[:space:]]+)*(go (test|vet)|gofmt|pre-commit run)([[:space:]]|$)'; then
	echo "Run the checks on their own, read the result, then commit in a separate command." >&2
	exit 2
fi
exit 0
