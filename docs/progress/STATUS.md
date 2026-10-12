# Status

Updated: 2026-10-12
Phase: released (v1.0.0, 2026-10-12)
Current card: none
Card state: ready
Branch: main
PR: 119 (site and README sync, v1.0.1 changelog section), after 118
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- `v1.0.1` (documentation release, so pkg.go.dev shows the current README). Merge PR 119; `main` is then the commit to tag. The release session tags all four modules from a new `release/v1.0.1` branch in its own worktree (docs/release.md steps 2 to 7), runs `make release-smoke VERSION=v1.0.1` and publishes the GitHub release. Nothing may ask the module proxy for `v1.0.1` before the tags are pushed (docs/release.md step 4), which is why `site/data/release.ts` and `WEIR_VERSION` in ci.yml still say `v1.0.0` here. After the tags, the site session opens the PR from `claude/v1.0.1-post-release` (both bumps) and redeploys the site.
- The site is live at https://weir.ashwinsathian.com (Pages project `weir-site`, direct upload; nothing deploys on merge). The About page (`site/content/pages/about.md`) is drafted in your voice and still wants your edit.

- `v1.0.0` is tagged for all four modules and published (docs/release.md, rehearsal log).
- `release/v1.0.0` is not the base for `v1.0.1`: cut `release/v1.0.1` from `main`, which has the new README. The old branch can go once nothing needs it (its tags keep the commits).
- PR 99 is closed; its six threat rows are in docs/06 as T-46 to T-51, checked against v1.0.0 (PR 118).
- The five behavior changes of PR 111 shipped in v1.0.0 (FR-KEY-5, FR-FWD-1, FR-RNG-6, `Entry.Size`, `valkey.Config.MaxObjectBytes`).

## Notes for the next session

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
