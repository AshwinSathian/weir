# Status

Updated: 2026-10-12
Phase: release
Current card: none
Card state: awaiting-merge
Branch: claude/rc3-rehearsal (rc.3 record, `release-smoke`)
PR: see the PR list
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Merge the rc.3 record PR (docs, `scripts/release-smoke.sh`, `WEIR_VERSION: v1.0.0-rc.3` in CI). No Go code.
- Then `v1.0.0` by docs/release.md steps 2 to 10, from the `main` commit the gate covers. The rc.3 rehearsal ran those steps without a change. The gate log in docs/release.md has the run for the rc.3 tree; a docs-only merge after it does not restart it.
- After the `v1.0.0` tags: `make release-smoke VERSION=v1.0.0`, runbook 7.8 on the built Caddy, pkg.go.dev pages, `WEIR_VERSION: v1.0.0`, date the CHANGELOG section, publish the GitHub release, delete `release/v1.0.0-rc.1` and `release/v1.0.0-rc.3`.
- Confirm the decisions taken for you: D24 amended, D43 added, README Stability promises, signing key caps, `servableStatus` miss.

- Decide on the review follow-ups listed in the last LOG entry (If-Match handling, unkeyed Pragma/Cache-Control storage, QuerySort order, Entry.Size accounting, Valkey record size limit).

## Notes for the next session

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: see the last LOG entry (export for `caddy`, `Event.Count`, zero-able knobs, prom options, apidiff job).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
