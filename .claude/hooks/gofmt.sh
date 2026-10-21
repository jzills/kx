#!/bin/sh
# PostToolUse on Edit|Write: report a Go file gofmt would change, without
# rewriting it. Rewriting a file under the agent leaves its view of the file
# stale, and its next Edit fails to match. Exit 2 sends the message back to
# the agent; anything that can't be checked is let through.
command -v jq >/dev/null 2>&1 || exit 0
file=$(jq -r '.tool_input.file_path // empty')
case "$file" in
*.go) ;;
*) exit 0 ;;
esac
[ -f "$file" ] || exit 0
if ! out=$(gofmt -l "$file" 2>&1); then
	printf '%s\n' "$out" >&2
	exit 2
fi
if [ -n "$out" ]; then
	printf "gofmt would change %s — run 'gofmt -w %s'.\n" "$file" "$file" >&2
	exit 2
fi
exit 0
