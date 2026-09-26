---
name: next-card
description: "Start the next Weir task card, or a named one. Checks open PRs, branches from main, loads only the card's reading list, then implements tests-first. Use at the start of every implementation session."
disable-model-invocation: true
argument-hint: "[card-id]"
---

# Start a card

Current state:

```!
cat docs/progress/STATUS.md 2>/dev/null || echo "STATUS.md missing"
git status --short --branch 2>/dev/null || true
gh pr list --state open --json number,title,headRefName,reviewDecision,url 2>/dev/null || echo "gh unavailable: check open PRs manually"
```

Requested card: $ARGUMENTS (empty means the next open card).

Follow these steps in order. Stop and tell the user whenever a step says so.

1. Open card PRs. If a PR from a `card/*` branch is open:
   - review decision `CHANGES_REQUESTED`, or the user asked you to address comments: check out that branch, read the comments (`gh pr view <n> --comments`), fix them test-first, then run `/handoff`. Do not start a new card.
   - otherwise: stop. Tell the user the PR is waiting for review or merge, with its URL. Cards build on each other; do not start the next one from an unmerged base.
2. Sync: `git switch main && git pull --ff-only`. If the tree is dirty, stop and ask.
3. Pick the card: the requested ID, else `scripts/card.sh next`. Print it with `scripts/card.sh <ID>`. Check every "Depends on" card is marked `[x]` (`scripts/card.sh list`); if not, stop and say which.
4. If the card or STATUS lists anything under "Waiting on Ashwin" that affects this card, ask now, before writing code.
5. Branch: `git switch -c card/<ID>-<short-slug>`. Update docs/progress/STATUS.md: Current card, Card state `in-progress`, Branch, Updated date. Do not commit yet.
6. Read only what the card lists. Use `scripts/section.sh docs/<file> <section>` for numbered sections and `grep -n` for requirement IDs and threat rows. Read the existing code of packages you will touch. Do not read whole design documents.
7. Write a plan of at most 10 lines in the conversation: files, tests, order.
8. Write the tests first (names from the card and docs/07, each with a comment citing requirement and threat IDs). Run them; confirm they fail for the right reason.
9. Implement the smallest code that passes. Iterate with `go test ./<pkg>/...`, not the full suite.
10. Budget checkpoint: if the card is clearly larger than its size (more files, far more code, or the context is filling), stop adding scope and run `/handoff split` (CLAUDE.md overrun rule). Never let the session compact mid-card.
11. When the card's tests pass, run `/handoff`.
