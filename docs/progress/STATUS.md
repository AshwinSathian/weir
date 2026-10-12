# Status

Updated: 2026-10-12
Phase: release
Current card: none
Card state: awaiting-merge
Branch: claude/v1-hardening (stacked on claude/rc1-rehearsal-fixes, PR 110)
PR: see the PR list
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Merge the hardening PR (it contains PR 110's commit; merging it alone is enough, or merge 110 first). A session cannot merge: the permission layer refused `gh pr merge`.
- Then, on the merged `main` commit, in this order: full `release-gate` run (Go code changed in `internal/keys`, `store`, `store/memory`, `store/valkey` and the root package, so the gate of 2026-10-12 no longer covers it); `v1.0.0-rc.3` for all four modules by the corrected procedure in docs/release.md (`caddy/v1.0.0-rc.2` is taken); set `WEIR_VERSION: v1.0.0-rc.3` in the `caddy-published` CI job.
- Review the five behavior changes in the hardening PR; each changes a requirement or a public contract (FR-KEY-5, FR-FWD-1, new FR-RNG-6, `Entry.Size`, new `valkey.Config.MaxObjectBytes`). Say so if one should be reverted before rc.3.
- After the `v1.0.0` tags: `WEIR_VERSION: v1.0.0`, date the CHANGELOG section, publish the GitHub release, delete the rc branches.
- Confirm the decisions taken for you: D24 amended, D43 added, README Stability promises, signing key caps, `servableStatus` miss.

- Decide on the review follow-ups listed in the last LOG entry (If-Match handling, unkeyed Pragma/Cache-Control storage, QuerySort order, Entry.Size accounting, Valkey record size limit).

## Notes for the next session

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: see the last LOG entry (export for `caddy`, `Event.Count`, zero-able knobs, prom options, apidiff job).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
