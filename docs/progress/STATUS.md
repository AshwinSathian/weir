# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/busy-tesla-iw6ola (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/104
Next card: none (`make next` says all cards are done)

## Waiting on Ashwin

Nothing.

## Notes for the next session

- P3-05 is done: the `experiments { ... }` block (Caddyfile and JSON) is in `caddy/{config,caddyfile,module}.go`; signing keys keep `{env.VAR}` and resolve at config build; experiments are outside the key-generation hash. docs/08 §2 and §3, runbook 7.9 updated. This was the last Phase 3 card; no card is open.
- Caddyfile parse errors in the whole `weir` block quote a token only when it looks like a directive name (`tok`), and never use Caddy's `ArgErr` (`argErr`), because a wrapped line can leave a password or signing key in a key position.
- `caddy validate` reads the operator's shell environment, so it fails on an unset `WEIR_EXP_KEY*` there (runbook 7.9).
- A signing key is a literal or exactly `{env.NAME}`; any other brace is refused at load (the earlier general replacer could read files or the clock into a key). A block with neither `signing_keys` nor an experiment is refused.
- Open follow-up (engine, not this card): `SigningKeys` has no maximum count and verification does one HMAC per key per request (NFR-3). Suggest a cap such as 4 in `internal/experiments/config.go`.
- Hit-path cost note from P3-04 still stands: a new matched visitor costs 48 allocs; a later card may cut it.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `make check GOLANGCI=/tmp/lintbin/golangci-lint` with `GOTOOLCHAIN=go1.27.0` (the installed binary is built with Go 1.25).
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake. Open follow-up from P25-07d: a way for `caddy validate` to skip the purge.
