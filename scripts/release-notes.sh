#!/usr/bin/env bash
# Prints the release notes file for a tag: docs/release-notes/<tag>.md,
# or for a prerelease tag without one of its own (v0.6.0-rc1) its
# version's file (v0.6.0.md). release.yml runs it twice: the windows job
# fails a tag early when there is none, and the release job publishes the
# file as the release's body (GitHub's generated "What's Changed" list is
# appended to it).
#
# It fails (exit 1, a ::error:: line) when there is no file, or when the
# file is still a draft: its first line starts with "<!-- draft".
#
#   scripts/release-notes.sh v0.6.0     # -> docs/release-notes/v0.6.0.md
set -euo pipefail

tag="${1:?usage: scripts/release-notes.sh <tag>}"
dir="$(cd "$(dirname "$0")/.." && pwd)/docs/release-notes"

file="$dir/$tag.md"
if [ ! -f "$file" ]; then
  file="$dir/${tag%%-*}.md"
fi
if [ ! -f "$file" ]; then
  echo "::error::no release notes for $tag: write docs/release-notes/${tag%%-*}.md (docs/release-notes/README.md) and merge it before tagging"
  exit 1
fi
if head -n 1 "$file" | grep -q '^<!-- draft'; then
  echo "::error::docs/release-notes/$(basename "$file") is still a draft: finish it and delete its first line (the <!-- draft ... --> comment) before tagging"
  exit 1
fi
echo "docs/release-notes/$(basename "$file")"
