---
name: handoff
description: "Finish the current Weir card with a full check, an independent review by the card-reviewer agent, progress updates, commit, push and PR. Use '/handoff split' when a card overran and must be split."
disable-model-invocation: true
argument-hint: "[split]"
---

# Hand off a card

Mode: $ARGUMENTS (empty means the card is complete; `split` means finish the part that is done and hand the rest to a new card).

```!
sed -n '1,12p' docs/progress/STATUS.md 2>/dev/null || true
git status --short --branch 2>/dev/null || true
git log --oneline main..HEAD 2>/dev/null | head -20 || true
```

1. Full check: `make check`. Fix until it passes. If a failure is outside this card's scope and pre-existing, do not fix it here: record it under Blockers in STATUS and tell the user.
2. Independent review: use the Agent tool with `subagent_type: card-reviewer` and a prompt containing the card ID, the base (`main`), and the list of changed files. Fix every finding marked `must-fix` (test first when it is a behavior bug). List `should-fix` findings you did not fix in the PR body with a reason.
3. Docs: if the implementation had to differ from a normative document, update that document now (CLAUDE.md step 5; ask the user first when it changes a requirement, default, decision table or public signature).
4. Split mode only: in the card's file, insert a new card `### [ ] <ID>b <remaining work>` right after the current card, with Plan, Size, Depends on `<ID>`, Read, Touch, Tests, AC, copied and trimmed to the remainder. The current card's AC must describe only what is done now; edit it to match.
5. Mark done: `scripts/card.sh done <ID>`. If every card mapped to a PLAN-weir.md task is now done, tick that task's checkbox.
6. Progress files:
   - STATUS.md: Current card `none`, Card state `awaiting-merge`, Branch, PR (fill after step 8), Next card (`scripts/card.sh next` after marking done), Updated date, Blockers, Waiting on Ashwin, Notes for the next session (only what the next session needs; 3-6 bullets).
   - LOG.md: append one entry using the template at the top of the file.
7. Commit on the card branch with a Conventional Commit message: `<type>(<milestone lowercased>): <what> [<card ID>]`, body with requirement IDs covered and, for security-sensitive cards, the docs/06 §6 checklist answers. End with the co-author trailer.
8. `git push -u origin card/<ID>-<slug>` (the literal branch name; it matches the project's permission allowlist, `HEAD` does not), then `gh pr create` with the template in `.github/pull_request_template.md` filled in. Put the PR URL into STATUS.md, commit (`chore(progress): record PR for <ID>`), `git push origin card/<ID>-<slug>`.
9. Final message to the user: PR URL, what was done, review findings left open, next card ID. Then stop. Do not start the next card in this session.
