# Status

Updated: 2026-10-11
Phase: 1
Current card: none
Card state: ready
Branch: claude/great-bardeen-t56hcm (designated session branch)
PR: none
Next card: P3-01 (`make next`)

## Waiting on Ashwin

Nothing.

## Notes for the next session

- P25-07d is done: `keyGenRetry` in `caddy/keygen.go`, started last in `Provision` when the check was skipped, stopped first in `Cleanup`. 08 §3 and runbook 8.7 describe it. No config field was added.
- `make check` stops at golangci-lint here (installed binary built with Go 1.25, module targets 1.27). gofmt, vet, `go test -race -shuffle=on` in the root and `caddy` modules and trace 146/146 pass. CI is the lint gate.
- Adversarial review of PR 97 done (decisions delegated by Ashwin; "Waiting on Ashwin" was empty). Implemented: a failed record write is retried too; retry attempts log at Debug, a failing purge once per distinct error; a cancelled attempt logs nothing; `TestProvisionStartsAndCleanupStopsKeyGenRetry` covers the wiring with a real handler, no seam needed.
- Open follow-up: a way for `caddy validate` to skip the purge (needs a signal from Caddy). The "forwarding rules differ" Warn still repeats while a purge keeps failing (rare).
- `TestE2EPurgeHerd` (real-clock caddytest) failed once in about six runs and passed on rerun, also on the parent commit; treat it as a known flake.
- P3-00 is done (docs only, committed on the designated branch, no PR opened yet). Spec 10 is v1.0 with E8 and E9. Next is P3-01; ask before adding any public Config field.
