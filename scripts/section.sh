#!/usr/bin/env bash
# Print one numbered section of a doc, up to the next heading of the same or higher level.
# Usage: scripts/section.sh docs/04-lld.md 6.4     (also works for "13" or "T6.2")
set -euo pipefail
file=$1; num=$2
awk -v num="$num" '
  /^#+ / {
    level = index($0, " ") - 1
    title = substr($0, level + 2)
    if (!found && (index(title, num " ") == 1 || index(title, num ". ") == 1)) { found = 1; start = level; print; next }
    if (found && level <= start) exit
  }
  found { print }' "$file"
