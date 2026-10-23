# Weir website

The site at [weir.ashwinsathian.com](https://weir.ashwinsathian.com). Static files, built with Next.js and hosted on Cloudflare Pages. It is not part of the Go module: its own `go.mod` (a stub with no code) keeps the go command from packing this directory into the module zip, and nothing here is imported by Weir.

## Run it

Node 24 (see `.nvmrc`) and pnpm.

```sh
pnpm install
pnpm dev          # http://localhost:3000, no search
pnpm build        # static site in out/, with CSP, link check and the search index
pnpm check        # style lint, claims register, benchmark data, README program
```

From the repository root, `make site-check` runs the whole gate and `make site-deploy` uploads `out/` with wrangler.

## Where content comes from

A fact has one home. The site reads the repository at build time and fails the build when an anchor it depends on is gone.

| Page | Source |
|---|---|
| Configuration reference | `docs/01-technical-spec.md` §6 |
| Threat table, invariants, assets | `docs/06-threat-model.md` |
| Conformance | `docs/cache-tests-expected-failures.md` |
| Benchmark tables | `docs/benchmarks.md` |
| Production, incidents, observability, parts of the Caddy guide | `docs/runbook.md` |
| Changelog and the Atom feed | `CHANGELOG.md` |
| Cache-Status and error tables | `cachestatus.go`, `errors.go` (values from code, meanings in `lib/tables.ts`) |
| Go programs | the README's program and `Example` functions in `example_test.go` |

Hand-written pages are Markdown in `content/docs/` and `content/pages/`, with `title` and `description` front matter. They can pull repository text in with directives, each alone on a line:

```text
{{include docs/runbook.md "## 4. Incidents" demote=-1}}   a section, by the start of its heading line
{{example example_test.go ExampleEngine_Serve}}           the body of a tested Example function
{{fenced README.md go}}                                   the first fenced block of that language
{{figure budget}}                                         a diagram from components/diagrams.tsx
{{figure results-failed}}                                 a chart from components/charts.tsx
```

`{{version}}`, `{{go_min}}`, `{{go_deploy}}` and `{{caddy_version}}` come from `data/release.ts`. Callouts use `> [!NOTE]`, `> [!CAUTION]`, `> [!PROTECTION]` and `> [!LIMITS]`. Requirement, threat and risk IDs in prose (`FR-COA-4`, `T-13`, `R-2`) become links automatically.

## Charts

The charts and headline tiles on the benchmarks and compare pages are static inline SVG, drawn at build time by `components/charts.tsx` from `lib/results.ts`, which parses the comparison tables of `docs/benchmarks.md`. There is no chart library and no data file.

- `lib/results.ts` names the exact header of every table it reads. A table that gains, loses or renames a column fails the build, and so does a sentence in a tile ("Varnish matches it") that the tables stop supporting.
- `components/charts.tsx` may not contain a digit in its copy. `pnpm check` enforces that, so a number cannot be typed beside a derived one.
- After a build, `pnpm check charts` reads the tables a second time, without the parser, and compares every number drawn in `out/` with its cell. It also fails when a charted table has a row or column no chart draws, and when a figure that shows another project lacks the "not verified" statement. `pnpm build` runs it.
- Each chart is drawn twice, for a wide and a narrow column, and CSS shows one. The default and tuned tiers are both in the page; two radio buttons and CSS show one, so nothing needs JavaScript. The probe grid uses the same two buttons for default and hardened configurations.
- Encoding: Weir is the accent color and every other cache the muted ink. Fill always counts something lost or not served, an outline counts something served, and a dashed outline a failure that is the correct result. A cell whose repetitions differed by more than a tenth carries their range under the number, and a cache that cannot run a scenario gets the words "not available", never a zero or a bar. Every bar starts at zero and is a share of its own row's total; there is no log scale.

## Rules the checks enforce

- No em dashes and none of the words the project style bans. At most one bold phrase per section.
- A measured figure in hand-written copy (a time, an allocation count, a count of four digits or more) must be listed in `content/claims.md` with its source, and must appear in that source.
- The README's Go program must compile against the working tree.
- Every internal link and anchor in the built site must resolve.

`content/claims.md` is not published. Statements about other projects on the compare page are listed there with the URL and the date they were read. Re-read them before changing that page.

## Release state

`data/release.ts` has one switch. `RELEASED = true` shows `VERSION` in every install line. `RELEASED = false` shows the release candidate and a banner. The `release-state` CI job runs `node scripts/check.mjs release`, which fails while the site claims a release that the Go module proxy cannot resolve for all four modules.

The production domain is attached only when that job is green (the launch gate in `docs/progress/surfaces-and-site-plan.md` §10).

## Deployment

Cloudflare Pages, direct upload, project `weir-site`. Chosen on 2026-10-12: Pages serves a static directory with `_headers` and `_redirects` and gives every branch a preview URL, which is all this site needs. There is no Worker and no build on Cloudflare's side.

```sh
make site-deploy BRANCH=main        # production: weir-site.pages.dev, and the custom domain once attached
make site-deploy BRANCH=site-launch # a preview URL
```

Security headers are in two places because a static export has inline bootstrap scripts. `scripts/postbuild.mjs` writes a Content-Security-Policy `<meta>` into each page with the SHA-256 of that page's inline scripts (no `unsafe-inline` for scripts), and writes `out/_headers` for the headers a `<meta>` cannot set. Styles allow `unsafe-inline` because the syntax highlighter and the search UI set style attributes.

No analytics, no cookies, no third-party requests. Fonts are self-hosted from `@fontsource`.

`public/.well-known/security.txt` points vulnerability reports to GitHub's private reporting. Its `Expires` line is one year out: move it forward with each release, and before 2027-10-23 at the latest.

Cloudflare must not inject anything into the pages: with Bot Fight Mode's JavaScript detections, Email Address Obfuscation or Web Analytics on, Cloudflare adds a script that the page's Content-Security-Policy blocks and that breaks the "no third-party requests" promise. Check with `curl -s https://weir.ashwinsathian.com/ | grep -c 'challenge-platform\|cloudflareinsights\|email-protection'`, which must print 0.

## Design

Dark by default with a stored light override. IBM Plex Sans and IBM Plex Mono. One accent (teal), amber only for "takes a protection back", red only for cautions. Tokens are at the top of `styles/site.css`.

Design references looked at on 2026-10-12, desktop, before choosing:

| Site | Taken | Avoided |
|---|---|---|
| hono.dev | definition left, working artefact right; one install line under the buttons | emoji feature cards |
| litestream.io | one calm column, one button, version state in a plain strip | |
| ghostty.org/docs, tailscale.com/docs | three zones: navigation, 720px prose, on-this-page; breadcrumbs | "Ask AI" widget |
| caddyserver.com | a real transcript as the hero's evidence | gradient, capitals, sponsor wall, star count |
| valkey.io | | cookie bar, slogan headline |

The home page figure (demand against origin load across a weir) is the one piece of imagery. Its shape is a schematic and its labels are measured numbers; keep it that way.

`public/og.png` is a 1200x630 screenshot of `scripts/og.html`.

## What is not here, on purpose

A blog, comments, accounts, a cookie banner, a docs version switcher (add one when a v2 exists), and an in-browser playground.
