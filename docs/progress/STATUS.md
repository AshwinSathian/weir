# Status

Updated: 2026-10-11
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/great-bardeen-t56hcm (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/98
Next card: P3-01 (`make next`)

## Waiting on Ashwin

Nothing.

## Notes for the next session

- P25-07d is done: `keyGenRetry` in `caddy/keygen.go`, started last in `Provision` when the check was skipped, stopped first in `Cleanup`. 08 §3 and runbook 8.7 describe it. No config field was added.
- `make check` stops at golangci-lint here (installed binary built with Go 1.25, module targets 1.27). gofmt, vet, `go test -race -shuffle=on` in the root and `caddy` modules and trace 146/146 pass. CI is the lint gate.
- Adversarial review of PR 97 done (decisions delegated by Ashwin; "Waiting on Ashwin" was empty). Implemented: a failed record write is retried too; retry attempts log at Debug, a failing purge once per distinct error; a cancelled attempt logs nothing; `TestProvisionStartsAndCleanupStopsKeyGenRetry` covers the wiring with a real handler, no seam needed.
- Open follow-up: a way for `caddy validate` to skip the purge (needs a signal from Caddy). The "forwarding rules differ" Warn still repeats while a purge keeps failing (rare).
- `TestE2EPurgeHerd` (real-clock caddytest) failed once in about six runs and passed on rerun, also on the parent commit; treat it as a known flake.
- P3-00 is done (PR 98). Spec 10 is v1.1 (E1 to E19). Cards P3-01 to P3-06. The public Config shape (`Config.Experiments *ExperimentsConfig`), `Event` fields and the other API choices were decided in the PR 98 review (delegated by Ashwin); P3-03 and P3-05 build on them. Threat rows T-46 to T-51 and docs/07 §13 hold the test names.
- CLAUDE.md rule 7 is untouched; E10 says identifiers use crypto/rand. Add a one-line note there if you want it explicit.
