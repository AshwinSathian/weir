#!/usr/bin/env bash
# Task-card helper. Cards live in docs/cards/*.md as level-3 headings:
#   ### [ ] M1-04 Title      (open)
#   ### [x] M1-04 Title      (done)
# Usage: card.sh <ID> | next | list | done <ID>
set -euo pipefail
cd "$(dirname "$0")/.."
files=$(ls docs/cards/[0-9]*.md 2>/dev/null | sort)
[ -n "$files" ] || { echo "no card files in docs/cards/"; exit 1; }

print_card() { # $1 = ID
  awk -v id="$1" '
    /^### \[[ x]\] / { h = $0; sub(/^### \[[ x]\] /, "", h); split(h, f, " "); show = (f[1] == id) }
    /^## / && show { show = 0 }
    show { print }' $files
}

case "${1:-}" in
  next)
    id=$(grep -hE '^### \[ \] ' $files | head -1 | awk '{print $4}' || true)
    [ -n "$id" ] || { echo "all cards done"; exit 0; }
    print_card "$id" ;;
  list)
    grep -hE '^### \[[ x]\] ' $files | sed -E 's/^### //' ;;
  done)
    [ -n "${2:-}" ] || { echo "usage: card.sh done <ID>"; exit 1; }
    f=$(grep -lE "^### \[ \] $2 " $files || true)
    [ -n "$f" ] || { echo "open card $2 not found"; exit 1; }
    sed -i.bak -E "s/^### \[ \] $2 /### [x] $2 /" "$f" && rm -f "$f.bak"
    echo "marked $2 done in $f" ;;
  "" ) echo "usage: card.sh <ID> | next | list | done <ID>"; exit 1 ;;
  *)
    out=$(print_card "$1"); [ -n "$out" ] || { echo "card $1 not found"; exit 1; }
    printf '%s\n' "$out" ;;
esac
