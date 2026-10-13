# Status

Updated: 2026-10-13
Phase: v1.1.0 released and deployed (2026-10-13)
Current card: CMP-03b and CMP-04b (one session and one branch, at the owner's request)
Card state: awaiting-merge
Branch: card/CMP-03b-compare-round-2
PR: opened by this session
Next card: none open. Candidates: CMP-F5 to CMP-F8 (decisions on the round 2 findings), CMP-06 (speed and memory), HRD-22 (share one over-size fetch), HRD-20 (deferred)

## Waiting on Ashwin

- Merge the round 2 pull request (CMP-03b, CMP-04b). The site is not deployed by it.
- Four findings about Weir from round 2 need a decision each: CMP-F5 (the per-path cap refuses a busy path while the origin has room), CMP-F6 (a soft purge leaves most URLs unrefreshed), CMP-F7 (the breaker fails clients after recovery when stale serving is off), CMP-F8 (a snapshot does not save origin calls after a restart). Options are on the cards in docs/cards/21-comparison.md.
- The cache-handler bug report is posted as https://github.com/darkweak/souin/issues/876 (open, no reply on 2026-10-13). Round 2 found a second behavior worth reporting there if you want to: a request for `/%70age` is stored under the key of `/page` and forwarded as spelled (docs/benchmarks.md, probe note (e)). Not reported.
- The site's chart branch (pull request 129) reads the comparison tables of docs/benchmarks.md. Round 2 changed their columns (no `Weir v1.0.1` column, `Weir v1.1.0` for `Weir v1.1`, new rows and a new table), so that branch needs to follow whichever of the two merges second.

## Notes for the next session

- Comparison runs: one `run.sh` call per subject and tier, and half the scenario list per call, keeps each under ten minutes (`bench/compare/results/r2.sh` on this machine did it; the directory is not committed). The full count-based pillar is about two and a half hours: 22 scenario calls, probes (cache-handler's three tiers take five minutes each for the Vary probe), conformance.
- Do not switch branches in the main checkout while another session may be using it. The round 2 session did, and the site session committed onto its branch; it had to be rebased off. Use `git worktree add`.
- Caddy's admin endpoint refuses requests that carry browser fetch metadata, so hand checks against `:2019` need a plain client (the Go runner, `wget`), not Node's `fetch`.

- `make vuln` needs Go 1.27.2 (`GOTOOLCHAIN=go1.27.2`): 1.27.1 reports nine standard library findings.
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
