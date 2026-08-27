#!/usr/bin/env bash
# Refuse to commit a file large enough to be a build artifact.
#
# tmp-plural-audit, a 65MB throwaway binary, reached the history in #251 beside
# a two-file source change and sat there for 27 commits — a quarter of the
# repository's packed size, and shipped inside v0.4.0's source archive.
#
# .gitignore now names the two shapes that caused it (/kx, /tmp-*), but an
# ignore rule only catches a name someone thought of ahead of time. This catches
# the shape instead: nothing kx tracks is close to the ceiling, and nothing it
# should track ever will be.
#
#   scripts/check_file_size.sh <file>...
#
# Run by the pre-commit hook of the same name, over staged files only.
set -euo pipefail

# 4MiB. The largest thing legitimately tracked is a 1.2MB demo GIF, so this
# leaves room for a bigger one without leaving room for a binary.
limit=$((4 * 1024 * 1024))

status=0
for file in "$@"; do
    # Deletions and submodules reach here as paths with nothing behind them.
    [ -f "$file" ] || continue

    size=$(wc -c <"$file")
    if [ "$size" -gt "$limit" ]; then
        printf '%s is %sMB, over the %sMB limit for a tracked file.\n' \
            "$file" "$((size / 1024 / 1024))" "$((limit / 1024 / 1024))"
        status=1
    fi
done

if [ "$status" -ne 0 ]; then
    printf '\nBuild artifacts do not belong in git. If this one does, raise the\nlimit in scripts/check_file_size.sh and say why.\n'
fi
exit "$status"
