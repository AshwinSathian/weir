# User-facing surfaces and website: decisions and launch gate

Updated: 2026-10-12
Status: implemented on branch `site/launch`. This file is a record, not a normative document, and is not part of the precedence order in docs/README.md.

The work followed a plan Ashwin supplied to the implementing session as `11-surfaces-and-site-plan.md` (sections 1 to 13: decisions, vocabulary, survey findings, drift control, surface inventory, content specification, site architecture, work packages S-01 to S-10, the truth pass, the launch gate, measures, an adversarial review and open questions). That file was an attachment and is not reproduced here; add it beside this one if you want the full text in the repository. Section numbers below match it, so "§10" in the site's README and CI comments means the launch gate in this file.

## Decisions taken on 2026-10-12

| Question | Decision |
|---|---|
| Scope | Everything in the plan's sections 5 to 10 in one branch and one pull request, README and site first |
| Q1 normative wording | Fix wording, tense and counts only; list each edit in the pull request. No requirement, default or signature changed |
| Q2 Valkey versions | State only what CI runs: Valkey 8.1 |
| Q3 comparison | Qualitative only, each statement sourced to the other project's own documentation and dated |
| Q4 behavior follow-ups | Five were fixed before the tag (PR 111). Cookie tossing is listed as a limit on the experiments page |
| Q5 styling | Plain CSS with custom properties, one stylesheet |
| Q6 Dockerfile | None |
| Q7 analytics | None |
| Q8 `/v1/` paths | Not now |
| Q9 `llms.txt` | Published, generated from the page index |
| Q10 user list | None |
| Q11 this file | Kept in `docs/progress/` |
| Q12 logo | A drawn mark: a weir in profile |
| Q13 content license | Apache-2.0 |
| Q14 About page | Three gaps only, no employer or product; first person, in Ashwin's voice |
| Q15 private vulnerability reporting | Verified enabled through the GitHub API on 2026-10-12 |
| Theme | Dark by default, stored light override (Ashwin, after seeing the first build) |
| Release flag | `RELEASED = true` from the start, by Ashwin's choice. All four `v1.0.0` tags were pushed during the session, so the `release-state` check passes |
| Domain | `weir-site.pages.dev` now; `weir.ashwinsathian.com` only when the gate below passes |
| CI weight | Lean gate in CI (source checks, type check, build, internal links, release state). Lighthouse and axe were not wired into CI |

## Deviations from the plan

- Content is Markdown rendered through unified, not MDX. Pages read repository documents at build time with include directives, so there is no `site/generated/` directory and no separate generator scripts: the build is the generator and fails on a missing heading.
- The Content-Security-Policy is a per-page `<meta>` with script hashes, written after the build, because one `_headers` line cannot hold every page's hashes.
- The 16-row "settings that take a protection back" table moved from the README to the site's tuning page, which is now its home (`site/content/docs/tuning.md`).
- `docs/benchmarks.md` gained a "Current results" section and an "Earlier runs" marker instead of a full rewrite; the run records are unchanged.
- Not done: Lighthouse CI budgets, axe in CI, `caddy adapt` on Caddyfile blocks in CI, a nightly external link check, per-page OG images (one static image is used), the design-reference notes for phone layouts, and the outside tester for the quickstart (§10.3).

## 10. Launch gate

Copy is written as if `v1.0.0` is out. These checks make the site safe to put on the production domain.

### 10.1 Before the production domain serves the site

1. All four tags exist and resolve from the Go module proxy: `v1.0.0`, `observe/prom/v1.0.0`, `store/valkey/v1.0.0`, `caddy/v1.0.0`. The `release-state` job in `.github/workflows/site.yml` checks this (`node site/scripts/check.mjs release`).
2. `make release-check` passes on the release branch.
3. `make release-smoke VERSION=v1.0.0` passes (docs/release.md step 9).
4. The items under "Waiting on Ashwin" in STATUS are closed.
5. `CHANGELOG.md` carries the release date.

State on 2026-10-12: items 1 and 5 hold (all four modules answer 200 at `v1.0.0` on proxy.golang.org, and `node site/scripts/check.mjs release` passes). Items 2 to 4 are Ashwin's to confirm.

### 10.2 If the tags slip

Leave the domain unattached. To publish earlier with working install lines, set `RELEASED = false` in `site/data/release.ts`: every install line shows `v1.0.0-rc.3` (all four rc.3 modules are on the proxy) and a pre-release banner appears. One switch, no copy edits.

### 10.3 After launch

Attach `weir.ashwinsathian.com` to the Pages project `weir-site`. Set the repository's website field. Submit the sitemap. Check that pkg.go.dev shows all four modules. Confirm the social preview on two platforms. Have someone who did not write the quickstart follow it, timed, before announcing anything.
