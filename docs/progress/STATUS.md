# Status

Updated: 2026-10-13
Phase: v1.1.0 in review (released: v1.0.1, 2026-10-12)
Current card: none
Card state: awaiting-merge
Branch: card/HRD-01-hardening
PR: https://github.com/AshwinSathian/weir/pull/122
Next card: HRD-18 (release v1.1.0), after the pull request is merged

## Waiting on Ashwin

- Review and merge the v1.1 hardening pull request (cards HRD-01 to HRD-17 and HRD-19 in docs/cards/22-hardening.md). It changes requirements, defaults and the exported API without the per-change question CLAUDE.md asks for, at your request to do the whole review in one pass. The 24 decisions are the table at the top of the cards file and in the pull request body: accept or reverse each before merging. The ones most likely to matter to an existing deployment: D-H1 (followers wait for a slow flight), D-H2 (`Breaker.MinPartitions` 2), D-H6 (a marker replaces a stale response), D-H12 (`TransportOrigin` strips forwarded fields on pass-through), D-H14 (Caddy placeholders), D-H16 (a `GET` with a body is passed through).
- After the merge, say so and HRD-18 runs the release: the `release-gate` workflow on the merged commit (fuzz 60 minutes a target, x100 under `-race`), `release/v1.1.0`, the `v1.1.0-rc.1` rehearsal, the four tags, the GitHub release, then the pull request that moves the site, the README install lines and the `caddy-published` job to `v1.1.0`, and `make site-deploy BRANCH=main`. Nothing was tagged in this session: tags go on a branch cut from the merged `main`, and module proxy entries cannot be replaced.
- `card/CMP-01-compare-harness` (comparison spec and harness) is still local in the main checkout, blocked on Docker Desktop. It carries its own STATUS and LOG entries and will conflict with this branch in `docs/progress/`; rebase it after this merges. Its early signal (Weir's default tier answers 503 to about 70% of clients in `sync-expiry`) is now runbook 4.8 and R-14.
- A pull request in `../ashwin-portfolio` updates the Weir write-up with the v1.1 numbers; it says "v1.1 (in review)" until the tag exists.

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
