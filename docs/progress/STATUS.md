# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/vigilant-tesla-7nuval (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/102
Next card: P3-04 (`make next`)

## Waiting on Ashwin

Nothing. The control-variant and hash-separator questions were decided in the PR 100 review (first listed variant; `salt || 0x00 || id`) and are in docs/10 v1.1.

## Notes for the next session

- P3-03 is done: `experimentResponse` (experiments.go) runs in `Serve` after the entry is stored, adds the assignment `Set-Cookie` (90-day Max-Age, from the PR 99 text, now in docs/10 v1.4) and rewrites Cache-Control to private plus drops CDN-Cache-Control. New public field `ExperimentsConfig.DownstreamKeysOnVariant` (approved); a response that sets the cookie is private even with it.
- P3-04 can read `Result.Cookie`, `Changed` and `MarkExposed` from `keys.Classified.Decision` in `experimentResponse`; an exposure mark also rewrites the cookie, so reuse the Set-Cookie code there.
- P3-05 must expose `downstream_keys_on_variant` in the Caddy block (noted on the card).
- Not done by design (E7 lists two fields only): `Surrogate-Control` and vendor CDN fields are left alone; documented in docs/10.
- Warm stores the control variant (or the winner) and Purge runs no assignment (docs/10 v1.3).
- Hit-path cost with experiments (`BenchmarkServeHitExperiments`): 14, 23 and 28 allocs/op before P3-03; matched requests now also clone the header map. P3-05 benchmarks may want to cut that.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` from a scratch directory, then `GOTOOLCHAIN=go1.27.0 /tmp/lintbin/golangci-lint run`. Run it before pushing.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
