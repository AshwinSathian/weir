# Status

Updated: 2026-10-13
Phase: v1.1.0 released and deployed (2026-10-13)
Current card: none (site work outside a card: the result charts, reworked for round 2)
Card state: awaiting-merge
Branch: site/results-round2
PR: opened by this session; it replaces pull request 129
Next card: none open. Candidates: a v1.2 release with a rerun of the comparison tables, CMP-06 (speed and memory), HRD-22 (share one over-size fetch), HRD-20 (deferred)

## Waiting on Ashwin

- Review and merge the site charts pull request (it replaces 129, which is closed). Then `make site-deploy BRANCH=main`. The branches `site/results-presentation` and `site/results-presentation-shots` can be deleted afterwards.
- The deployed site will describe `main` in a few doc pages that are ahead of `v1.1.0`; each such place is marked "next release". Releasing v1.2 removes the need for those notes: grep `site/content` for "next release".
- HRD-23 changes three defaults you approved on 2026-10-13 (`Breaker.OpenFor` 1 s, `Breaker.MaxOpenFor` equal to it, borrowing past the per-path cap for every client's fetch) and adds two opt-in settings (`Limiter.AdaptiveMax`, `SnapshotKeepFreshness`). They ship in the next release; the changelog's Unreleased section lists them with the way back for each.
- The published comparison tables still describe v1.1.0. After a v1.2 release, rerun them (11 §2 measures published modules); card HRD-23 holds the working-tree figures to expect.
- The cache-handler bug report is posted as https://github.com/darkweak/souin/issues/876 (open, no reply on 2026-10-13). Round 2 found a second behavior worth reporting there if you want to: a request for `/%70age` is stored under the key of `/page` and forwarded as spelled (docs/benchmarks.md, probe note (e)). Not reported.
- The site's chart branch (pull request 129) reads the comparison tables of docs/benchmarks.md. Round 2 changed their columns (no `Weir v1.0.1` column, `Weir v1.1.0` for `Weir v1.1`, new rows and a new table), so that branch needs to follow whichever of the two merges second.

## Notes for the next session

- Before a v1.2 release rerun `make load`: the busting-flood scenario's record in docs/benchmarks.md and the sentence on the site's benchmarks page ("held to its per-path cap of 8") describe v1.1. On this branch the flood borrows idle slots up to 40 and normal p99 under flood measured 74 µs against 45 µs alone (inside the test's bound, machine not idle).
- `make check` does not run the `integration` or `load` tagged tests. A change to limiter or breaker behavior needs `make test-valkey` against a local Valkey (`docker run -p 127.0.0.1:6379:6379 valkey/valkey:8.1 valkey-server --maxmemory-policy volatile-lfu`) and the affected `loadtest` scenarios before a push; CI caught one of each kind missed here.
- HRD-23 follow-ups the reviewer raised and this card left: the refresh backlog has no gauge (`limiter.Counts` reports the clients' queue only); `refresh-dropped/no-slot` has no test for the one-minute wait running out; a kept snapshot trusts a restored old file up to each entry's own expiry (no maximum file age); `limited` is also set when `MaxPerHost` alone held a fetch whose partition is at its cap.
- To measure an unreleased engine change against the comparison harness: `SUBJECTS=caddy-weir-next TIERS=default PATH="$PATH:$HOME/go/bin" ./run.sh -reps 3 -scenario <names>` from `bench/compare`. An opt-in Caddy setting needs a temporary tier directory under `subjects/caddy-weir/` (not committed).

- Comparison runs: one `run.sh` call per subject and tier, and half the scenario list per call, keeps each under ten minutes (`bench/compare/results/r2.sh` on this machine did it; the directory is not committed). The full count-based pillar is about two and a half hours: 22 scenario calls, probes (cache-handler's three tiers take five minutes each for the Vary probe), conformance.
- Do not switch branches in the main checkout while another session may be using it. The round 2 session did, and the site session committed onto its branch; it had to be rebased off. Use `git worktree add`.
- Caddy's admin endpoint refuses requests that carry browser fetch metadata, so hand checks against `:2019` need a plain client (the Go runner, `wget`), not Node's `fetch`.

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
