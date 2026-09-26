#!/usr/bin/env bash
# SessionStart: print current progress so the session starts oriented.
# Plain stdout is added to Claude's context. Keep it short.
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
[ -f docs/progress/STATUS.md ] || exit 0

echo "=== Weir progress (docs/progress/STATUS.md) ==="
cat docs/progress/STATUS.md
echo
echo "=== Git ==="
git status --short --branch 2>/dev/null | head -15
echo
card=$(sed -n 's/^Current card: //p' docs/progress/STATUS.md | head -1)
[ -z "$card" ] || [ "$card" = "none" ] && card=$(sed -n 's/^Next card: //p' docs/progress/STATUS.md | head -1)
if [ -n "$card" ] && [ "$card" != "none" ] && [ -x scripts/card.sh ]; then
  echo "=== Card $card ==="
  scripts/card.sh "$card" 2>/dev/null || echo "(card $card not found)"
  echo
fi
if [ -f docs/progress/LOG.md ]; then
  echo "=== Last LOG entry ==="
  awk '/^## /{buf=""} {buf = buf $0 "\n"} END {printf "%s", buf}' docs/progress/LOG.md
fi
echo
echo "Session protocol: CLAUDE.md > Session protocol. Start work with /next-card, end with /handoff."
exit 0
