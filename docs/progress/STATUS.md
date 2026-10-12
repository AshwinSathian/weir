# Status

Updated: 2026-10-12
Phase: release
Current card: none
Card state: awaiting-merge
Branch: claude/rc1-rehearsal-fixes (rc.1 rehearsal findings)
PR: see the PR list
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Merge the rehearsal PR. Then choose: go straight to `v1.0.0`, or rehearse the corrected procedure once more as `v1.0.0-rc.3` for all four modules (`caddy/v1.0.0-rc.2` is taken). The rc.1 code is sound; only the procedure and docs changed, so rc.3 is optional.
- Before the real tag: one full `release-gate` run on the final `main` commit (it now uses Go 1.27.2 or later; the last green run was `92a1e56` on 1.27.0), and `make load` on the reference machine if benchmarks.md should carry a 1.27.2 run.
- After the `v1.0.0` tags: set `WEIR_VERSION: v1.0.0` in the `caddy-published` CI job, date the CHANGELOG section, publish the GitHub release, delete `release/v1.0.0-rc.1`.
- Decide whether `js/wasm` and `wasip1/wasm` should build (the snapshot loader's `syscall.O_NONBLOCK` stops them). Documented as a known limit for v1.0.0.
- Confirm the decisions taken for you: D24 amended, D43 added, README Stability promises, signing key caps, `servableStatus` miss.

- Decide on the review follow-ups listed in the last LOG entry (If-Match handling, unkeyed Pragma/Cache-Control storage, QuerySort order, Entry.Size accounting, Valkey record size limit).

## Notes for the next session

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: see the last LOG entry (export for `caddy`, `Event.Count`, zero-able knobs, prom options, apidiff job).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
