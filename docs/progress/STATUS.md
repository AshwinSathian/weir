# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/gifted-mayer-b8afaw (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/103
Next card: P3-05 (`make next`)

## Waiting on Ashwin

One question from the P3-04 review: docs/10 §4 step 7 says the hashed `visitor` lets analysis deduplicate clients that refuse cookies. It cannot: such a client sends no cookie, so every request mints a new visitor id and a new hash. I reworded the P3-04 detail text to say so and left the original rationale sentence alone. Options: accept the inflated count, or drop the sentence. Not blocking P3-05.

## Notes for the next session

- P3-04 is done: `Assign` returns `Result.Exposures` (marked in the cookie, `Changed` set); `experimentResponse` emits `EvExposure{Experiment, Variant, Visitor}` through `emitExposures` for a returned response. `Event` has three new string fields (docs/04 §9.1, §9.2). `observe/prom` ignores the kind.
- P3-05 must expose `downstream_keys_on_variant` in the Caddy block (noted on the card). Cookies in tests now carry the exposed mark by default (`visitor`, `experimentsCookieFor`); `visitorMark(..., false)` builds an unmarked one.
- Hit-path cost: `Event` grew by 48 bytes; `-benchmem` was not re-run for P3-04. P3-05 benchmarks may check it, along with the P3-03 alloc figures (14, 45, 35 allocs/op).
- Not covered by a test (reviewer nits): the Encode-failure path and concurrent first requests both repeat the exposure with the same `visitor`; the docs/10 text says so.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` from a scratch directory, then `GOTOOLCHAIN=go1.27.0 /tmp/lintbin/golangci-lint run`. `make check` fails here because the installed binary is built with Go 1.25.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
