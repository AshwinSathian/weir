# Status

Updated: 2026-10-12
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/busy-tesla-iw6ola (designated session branch)
PR: (filled after push)
Next card: none (`make next` says all cards are done)

## Waiting on Ashwin

Nothing.

## Notes for the next session

- P3-05 is done: the `experiments { ... }` block (Caddyfile and JSON) is in `caddy/{config,caddyfile,module}.go`; signing keys keep `{env.VAR}` and resolve at config build; experiments are outside the key-generation hash. docs/08 §2 and §3, runbook 7.9 updated. This was the last Phase 3 card; no card is open.
- Parse errors in the `experiments` block never quote a token (`subBlockQuiet`), because a wrapped signing-key list can leave a key on its own line. The rest of the `weir` block still quotes unknown keys at its top level.
- `caddy validate` reads the operator's shell environment, so it fails on an unset `WEIR_EXP_KEY*` there (runbook 7.9).
- A literal signing key containing `{...}` is altered by placeholder expansion (the replacer expands all placeholders, not only `{env.*}`). Harmless given the 32-byte check; not changed.
- Hit-path cost note from P3-04 still stands: a new matched visitor costs 48 allocs; a later card may cut it.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `make check GOLANGCI=/tmp/lintbin/golangci-lint` with `GOTOOLCHAIN=go1.27.0` (the installed binary is built with Go 1.25).
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake. Open follow-up from P25-07d: a way for `caddy validate` to skip the purge.
