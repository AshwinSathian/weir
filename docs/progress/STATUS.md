# Status

Updated: 2026-10-13
Phase: v1.1.0 release in progress (released: v1.0.1, 2026-10-12)
Current card: HRD-18 (release v1.1.0)
Card state: in-progress
Branch: docs/v1.1.0-changelog
PR: https://github.com/AshwinSathian/weir/pull/126
Next card: none

## Waiting on Ashwin

- Merge the pull request that dates the CHANGELOG section (docs/release.md step 1). The tags are cut from `main` after it, once the `release-gate` run on `0e581b1` is green: https://github.com/AshwinSathian/weir/actions/runs/38369566716
- After the tags: one more pull request moves the site, the README install lines and the `caddy-published` job to `v1.1.0`, then `make site-deploy BRANCH=main`.
- The pull request in `../ashwin-portfolio` gets the released wording after the tag; merge it then.
- Your call: report to cache-handler that v0.17.0 answers 200 with an empty body to clients that waited on a fetch it did not store (docs/benchmarks.md note (a)).

## Notes for the next session

- HRD-18: follow docs/release.md from step 1. The CHANGELOG's "Unreleased" heading becomes `v1.1.0 (date)` in the step 1 pull request. `make vuln` needs Go 1.27.2 (`GOTOOLCHAIN=go1.27.2`): 1.27.1 reports nine standard library findings.
- The reference machine was not idle for the v1.1 numbers (docs/benchmarks.md says so). Rerun `benchstat` and `make load` on an idle machine before the tag if the record should replace the 2026-10-11 baseline.
- A process started in the background by the agent harness ran `make load` at a fraction of its speed (macOS priority). Run load and benchmarks in the foreground.
- Deferred: HRD-20 (coalescing across nodes, a local tier for remote stores).
- Site work: `make site-check` (Node 24, pnpm) is the gate; `make site-deploy BRANCH=main` uploads with wrangler. `site/README.md` explains the include directives and the claims register. Not done: Lighthouse and axe in CI, per-page OG images, an outside tester on the quickstart.
- Use a separate worktree for site or doc work when another session is using this checkout.

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: export for `caddy` (lifts the same-version constraint), `Event.Count`, zero-able knobs, prom options, the `apidiff` job against `v1.0.0`, `max_object_bytes` for the Caddy `store valkey` block, a decode failure on a planted Valkey value as a miss instead of `ErrUnavailable`.
- From the T-46 to T-51 review: `ExperimentsConfig` has no redacting `String`/`LogValue` (06 R-9, v1.1 candidate, public API addition); a `__Host-` assignment cookie name is accepted but not the default (06 R-10).
- Releases: use a separate worktree, never probe the proxy for a version before its tag, and keep `set -o pipefail` (docs/release.md steps 4 and 5).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
