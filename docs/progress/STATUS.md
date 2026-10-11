# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/gracious-shannon-w5n6k7 (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/100
Next card: P3-02 (`make next`)

## Waiting on Ashwin

Nothing. The control-variant and hash-separator questions were decided in the PR 100 review (first listed variant; `salt || 0x00 || id`) and are in docs/10 v1.1.

## Notes for the next session

- P3-01 is done: `internal/experiments` has config, cookie and pick, nothing wired into the engine. `Validate` fills in no defaults; P3-02 decides them and the public shape (ask first).
- The load limits are now in docs/10 §3 (v1.1). Consent (E6) is not in the package yet; it belongs to P3-02, with the defaults and the public shape (ask first). See the P3-02 card notes for the header-spoofing and allocation points from the review.
- `make check` stops at golangci-lint here (binary built with Go 1.25, module targets 1.27). gofmt, vet, `go test -race -shuffle=on ./...` and trace 146/146 pass. CI is the lint gate.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
