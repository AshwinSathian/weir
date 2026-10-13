# Status

Updated: 2026-10-13
Phase: v1.1.0 released and deployed (2026-10-13)
Current card: none
Card state: ready
Branch: main
PR: none
Next card: none open. Candidates: CMP-03b, CMP-04b, CMP-06 (comparison), HRD-22 (share one over-size fetch), HRD-20 (deferred)

## Waiting on Ashwin

- Nothing blocks. Pick the next card when you want one.
- Pull request 129 (`site/results-presentation`, site work outside a card): charts and headline tiles for the benchmarks and compare pages. Review and merge, then `make site-deploy BRANCH=main`. Delete the branch `site/results-presentation-shots` (screenshots only) afterwards.
- A bug report to cache-handler (v0.17.0 answers 200 with an empty body to clients that waited on a fetch it did not store, docs/benchmarks.md note (a)) is drafted for your review before it is posted.

## Notes for the next session

- `make vuln` needs Go 1.27.2 (`GOTOOLCHAIN=go1.27.2`): 1.27.1 reports nine standard library findings.
- The reference machine was not idle for the v1.1 numbers (docs/benchmarks.md says so). Rerun `benchstat` and `make load` on an idle machine before the tag if the record should replace the 2026-10-11 baseline.
- A process started in the background by the agent harness ran `make load` at a fraction of its speed (macOS priority). Run load and benchmarks in the foreground.
- Deferred: HRD-20 (coalescing across nodes, a local tier for remote stores).
- Site work: `make site-check` (Node 24, pnpm) is the gate; `make site-deploy BRANCH=main` uploads with wrangler. `site/README.md` explains the include directives and the claims register. Not done: Lighthouse and axe in CI, per-page OG images, an outside tester on the quickstart.
- Use a separate worktree for site or doc work when another session is using this checkout.
- Charts on the site are drawn from `docs/benchmarks.md` by `site/lib/results.ts`. A comparison table that changes its columns, or a rerun that contradicts a headline tile, fails `make site-check` on purpose: update `HEAD` in that file or the tile copy in `site/components/charts.tsx` with the data (`site/README.md`, "Charts").
- The site worktree for pull request 129 is `../weir-site-results`; remove it with `git worktree remove` after the merge. The first commit of that pull request also sits on the local branch `card/CMP-03b-compare-round-2` with the same SHA, because that session switched the main checkout while the site session was committing.

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: export for `caddy` (lifts the same-version constraint), `Event.Count`, zero-able knobs, prom options, the `apidiff` job against `v1.0.0`, `max_object_bytes` for the Caddy `store valkey` block, a decode failure on a planted Valkey value as a miss instead of `ErrUnavailable`.
- From the T-46 to T-51 review: `ExperimentsConfig` has no redacting `String`/`LogValue` (06 R-9, v1.1 candidate, public API addition); a `__Host-` assignment cookie name is accepted but not the default (06 R-10).
- Releases: use a separate worktree, never probe the proxy for a version before its tag, and keep `set -o pipefail` (docs/release.md steps 4 and 5).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
