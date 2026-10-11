# Status

Updated: 2026-10-11
Phase: 3
Current card: none
Card state: awaiting-merge
Branch: claude/awesome-ritchie-8ltcfc (designated session branch)
PR: https://github.com/AshwinSathian/weir/pull/101
Next card: P3-03 (`make next`)

## Waiting on Ashwin

Nothing. The control-variant and hash-separator questions were decided in the PR 100 review (first listed variant; `salt || 0x00 || id`) and are in docs/10 v1.1.

## Notes for the next session

- P3-02 is done: `Config.Experiments`, key dimension, variant header and cookie strip are wired. `keys.Classified.Decision` holds an `experiments.Result` (visitor id, updated cookie, `Changed`) for P3-03's Set-Cookie; nothing sets a cookie yet.
- `Warm` classifies without a cookie, so with experiments on and no consent gate it warms a random variant. P3-03/04 should decide (control is the likely answer). Purge also runs Assign through Classify, harmlessly.
- Hit-path cost with experiments (`BenchmarkServeHitExperiments`): 14, 23 and 28 allocs/op for unmatched, matched without cookie and matched with a valid cookie. P3-05 benchmarks may want to cut `Active`, `Picks` and the HMAC allocations.
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` from a scratch directory, then `GOTOOLCHAIN=go1.27.0 /tmp/lintbin/golangci-lint run`. Run it before pushing.
- `TestE2EPurgeHerd` (real-clock caddytest) is a known flake.
- Open follow-up from P25-07d: a way for `caddy validate` to skip the purge (needs a signal from Caddy).
