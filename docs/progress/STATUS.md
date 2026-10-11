# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/gracious-shannon-w5n6k7 (designated session branch)
PR: PR_URL
Next card: P3-02 (`make next`)

## Waiting on Ashwin

- docs/10 never says which variant is the control (E6 consent fallback). P3-01 uses the first listed variant. OK to add "the first listed variant is the control" to 10 §3 / §4 step 3?
- `Pick` hashes `salt || visitor-id` exactly as 10 §4 step 3 reads, with no separator, so (salt `ab`, id `c`) equals (`a`, `bc`). Ids are fixed-length in practice. Keep as is, or add a NUL separator to the spec?

## Notes for the next session

- P3-01 is done: `internal/experiments` has config, cookie and pick, nothing wired into the engine. `Validate` fills in no defaults; P3-02 decides them and the public shape (ask first).
- Limits added without a spec line (document in 10 §3 with the P3-02 edit): signing key at least 16 bytes, names at most 32 bytes of `[A-Za-z0-9_-]`, weights 1 to 1000, total at most 10000, cookie at most 512 bytes. Consent (E6) is not in the package yet; it belongs to P3-02.
- `make check` stops at golangci-lint here (binary built with Go 1.25, module targets 1.27). gofmt, vet, `go test -race -shuffle=on ./...` and trace 146/146 pass. CI is the lint gate.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
