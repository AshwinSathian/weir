# Status

Updated: 2026-10-12
Phase: release
Current card: none (surfaces and website, outside the card system)
Card state: awaiting-merge
Branch: site/launch
PR: see the PR list ("docs(site): website, README and user-facing surfaces")
Next card: none (all cards done; M16-02 deferred)

## Waiting on Ashwin

- Review and merge the `site/launch` PR: new README, `site/` (Next.js static site), truth-pass wording in docs 01, 03, PLAN, runbook, benchmarks, CHANGELOG, plus CONTRIBUTING, CODE_OF_CONDUCT, issue templates and `site.yml`. The PR body lists every normative-document edit.
- The site is deployed to `weir-site.pages.dev` and says `v1.0.0` everywhere. All four `v1.0.0` tags resolve from the Go proxy (checked 2026-10-12), so the `release-state` job passes. Before announcing: confirm launch-gate items 2 to 4 and run the post-launch list (docs/progress/surfaces-and-site-plan.md §10).
- The About page is drafted in your voice; edit it before the domain goes live.

- Merge the rc.3 record PR (docs, `scripts/release-smoke.sh`, `WEIR_VERSION: v1.0.0-rc.3` in CI). No Go code.
- Then `v1.0.0` by docs/release.md steps 2 to 10, from the `main` commit the gate covers. The rc.3 rehearsal ran those steps without a change. The gate log in docs/release.md has the green run for the rc.3 tree (38259811052, all 35 jobs); a docs-only merge after it does not restart it.
- After the `v1.0.0` tags: `make release-smoke VERSION=v1.0.0`, runbook 7.8 on the built Caddy, pkg.go.dev pages, `WEIR_VERSION: v1.0.0`, date the CHANGELOG section, publish the GitHub release, delete `release/v1.0.0-rc.1` and `release/v1.0.0-rc.3`.
- Confirm the decisions taken for you: D24 amended, D43 added, README Stability promises, signing key caps, `servableStatus` miss.

- Decide on the review follow-ups listed in the last LOG entry (If-Match handling, unkeyed Pragma/Cache-Control storage, QuerySort order, Entry.Size accounting, Valkey record size limit).

## Notes for the next session

- Site work: `make site-check` (Node 24, pnpm) is the gate; `make site-deploy BRANCH=main` uploads with wrangler. `site/README.md` explains the include directives and the claims register.
- Not done for the site: Lighthouse and axe in CI, per-page OG images, an outside tester on the quickstart. The original plan file was a session attachment and is not in the repository; `docs/progress/surfaces-and-site-plan.md` records the decisions and the launch gate.

- The sub-modules require weir v0.0.0 with `replace` on `main`; the pinned `go.mod` files live only on a release branch. `make release-check` fails on `main` by design. The four `v1.0.0-rc.1` modules are on the proxy and work for outsiders (docs/release.md, rehearsal log).
- `GOTOOLCHAIN=go1.27.2` runs everything on a machine whose installed Go is older.
- Deferred to v1.1: see the last LOG entry (export for `caddy`, `Event.Count`, zero-able knobs, prom options, apidiff job).
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8).
- Lint can run here: `GOTOOLCHAIN=go1.27.0 GOBIN=/tmp/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `GOTOOLCHAIN=go1.27.0 make check GOLANGCI=/tmp/lintbin/golangci-lint`.
- `TestE2EPurgeHerd` (real-clock caddytest) did not flake in 15 local runs.
