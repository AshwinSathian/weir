# Status

Updated: 2026-10-12
Phase: 3
Current card: P3-05
Card state: in-progress
Branch: claude/busy-tesla-iw6ola (designated session branch)
PR: none yet (code and tests done; `/handoff` pending)
Next card: P3-05 (`make next`)

## Waiting on Ashwin

Nothing. The P3-04 review question (cookie-refusing clients cannot be deduplicated by the hashed visitor) was decided with Ashwin's delegation: accept the inflated count, correct the docs/10 step 7 rationale, keep emitting on first visits.

## Notes for the next session

- P3-04 is done: `Assign` returns `Result.Exposures` (marked in the cookie, `Changed` set); `experimentResponse` emits `EvExposure{Experiment, Variant, Visitor}` through `emitExposures` for a returned response. `Event` has three new string fields (docs/04 §9.1, §9.2). `observe/prom` ignores the kind.
- P3-05 must expose `downstream_keys_on_variant` in the Caddy block (noted on the card). Cookies in tests now carry the exposed mark by default (`visitor`, `experimentsCookieFor`); `visitorMark(..., false)` builds an unmarked one.
- Hit-path cost with P3-04 (adversarial review, main vs branch): unchanged for hits (14 allocs); matched-no-cookie 45 to 48 allocs (the hash, hex and `Exposures` slice for a new visitor); matched-valid-cookie 35 allocs once the benchmark cookie carries the mark. P3-05 benchmarks may want to cut the new-visitor cost.
- Exposure repeats with one hash are documented (docs/10 step 7): concurrent first requests with an unmarked cookie, and an E9-dropped assignment. `TestExposureRepeatsAfterE9Drop` pins the second.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` from a scratch directory, then `GOTOOLCHAIN=go1.27.0 /tmp/lintbin/golangci-lint run`. `make check` fails here because the installed binary is built with Go 1.25.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
