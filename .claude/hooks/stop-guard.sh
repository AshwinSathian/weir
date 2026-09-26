#!/usr/bin/env bash
# Stop: fires at the end of every turn, so it must not nag mid-card.
# Blocks only when a card branch has commits, the tree is clean (work looks finished),
# and docs/progress/LOG.md was not updated on the branch. Exit 2 + stderr blocks.
input=$(cat)
case "$input" in *'"stop_hook_active":true'*|*'"stop_hook_active": true'*) exit 0 ;; esac
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || exit 0
case "$branch" in card/*) ;; *) exit 0 ;; esac
[ -z "$(git status --porcelain 2>/dev/null)" ] || exit 0
base=$(git merge-base HEAD origin/main 2>/dev/null || git merge-base HEAD main 2>/dev/null) || exit 0
[ -n "$(git rev-list "$base"..HEAD 2>/dev/null)" ] || exit 0
if git diff --name-only "$base"..HEAD | grep -qx 'docs/progress/LOG.md'; then exit 0; fi
cat >&2 <<MSG
Card branch $branch has commits but docs/progress/LOG.md was not updated on it.
Before ending the session run /handoff (or /handoff split if the card overran).
If you are only pausing to wait for the user, append a LOG entry with outcome "blocked" and the question, update STATUS, commit, then stop.
MSG
exit 2
