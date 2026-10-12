# User-facing surfaces and website plan

Updated: 2026-10-12
Status: plan, reviewed adversarially (section 12), not yet implemented. Nothing in this file changes a requirement, a default or a public signature.

This is the brief for one piece of work: bring every surface a stranger touches into line with what Weir is today, and build the website at `weir.ashwinsathian.com`. It is written so Claude Code can implement it in slices without re-deriving the reasoning. Facts quoted here were checked against the repository at `831c2b3` on 2026-10-12 unless a line says "verify".

## 1. Decisions already taken

| Decision | Source |
|---|---|
| Site lives in `/site`, built with Next.js, deployed on Cloudflare at `weir.ashwinsathian.com` | Ashwin, this session |
| Copy is written as if `v1.0.0` is out | Ashwin, this session (see the launch gate in section 10, because it is not out) |
| The site has an honest comparison page that says where Weir is the wrong choice | Ashwin, this session |
| The deliverable of this session is the plan; the build happens in Ashwin's own Claude Code sessions | Ashwin, this session |
| Root module stays standard-library only; `/site` is not a Go module and must not touch `go.work` | CLAUDE.md rule 1 |
| Writing style: plain, no inflated vocabulary, no em dashes, sentence-case headings, at most one bold phrase per section | CLAUDE.md |

Open questions are collected in section 13. Each has a recommended default so work is not blocked.

## 2. What Weir is, in the words the surfaces should use

**The one-sentence definition.** Weir is a Go library for shared HTTP caching in front of an origin that has to stay up.

**The fear it answers in sentence two.** The forwarded request is the request that was keyed, so a header the cache ignores cannot poison an entry, and a stampede cannot reach the origin faster than a budget you set.

**Who it is for.** Go teams running a reverse proxy or API gateway (net/http or Caddy) that caches, and hit one of these: many keys expiring together, a purge that turns into a stampede, a cache-busting query flood, a storage outage that fails open, or unkeyed-input poisoning.

**Who it is not for** (the site must say this plainly, see 6.4): people who want a CDN, a WAF, a rate limiter, a private (browser) cache, a distributed cache for application data, or a drop-in Varnish replacement with VCL.

**Message hierarchy** (every surface follows this order, depth increasing):

1. What it is (one sentence).
2. Why you would care (the five failure modes, with numbers from our own tests).
3. How little it takes to try (10 lines of Go, or 6 lines of Caddyfile).
4. Why you can trust it (conformance table, threat model, fuzz and race gates, honest limits).
5. When not to use it.

**Vocabulary to hold fixed.** "Weir" (never "WEIR" or "the Weir"). "Origin". "Shared cache". "Coalescing" (not "collapsing" or "single-flight", mention those once as synonyms). "Soft purge" and "hard purge". "Stale-while-revalidate" and "stale-if-error" written in full on first use per page, then SWR and SIE. "Import path `github.com/AshwinSathian/weir/caddy`, Go package `weircaddy`" wherever the Caddy module is named.

## 3. What the survey found

Sources: a research pass over comparable projects' READMEs and docs (web fetch limited to GitHub and pkg.go.dev in the sandbox; many marketing sites could not be reached, so visual design, mobile behavior, accessibility and navigation of those sites are unverified and listed as a task in 8.3), plus a fact sheet of this repository.

### 3.1 Done right elsewhere (adopt)

| Pattern | Seen in | Weir application |
|---|---|---|
| Definition, then the reader's biggest fear answered in sentence two | Litestream | Section 2 |
| A "what it does not do" list | groupcache, Ristretto FAQ | Non-goals block on README and home |
| Concrete caveats on the page, including surprising behavior | Ristretto (dropped Sets), Cloudflare docs (`s-maxage` disables SWR) | An "interactions and surprises" page |
| Method named beside benchmark numbers | Otter | Hardware, Go version, command, raw table, "what this does not show" |
| Two-latest-Go support policy stated | Otter | Already D42; state it on README and site |
| Explicit experimental labels | Pingora | Mark Experiments and the Caddy directive-order registration honestly |
| A worked arithmetic example of a timing knob | Varnish grace + keep | Timeline diagrams for SWR, SIE, jitter, early refresh |
| A demo directly under the tagline | Bubble Tea | A recorded stampede: 1,000 requests, 1 origin call |
| Checkable claims ("zero dependencies") | Hono | "Standard library only" is enforced by `deps_test.go`; link it |
| Runnable `examples/` and tested `Example` functions | sturdyc, x/sync | Pull site code from `example_test.go` so it cannot drift |
| One short Caddyfile example with defaults stated | caddyserver/cache-handler | Caddy quickstart |

### 3.2 Done wrong elsewhere (avoid)

| Anti-pattern | Seen in | Rule for Weir |
|---|---|---|
| Badge wall and sponsor banner above the description | ATS, Caddy, Hono | At most four badges, below the first paragraph |
| Contradictory sections and stale examples | Souin storage text, ATS version numbers, sturdyc signatures | Single source per fact; generated or tested snippets; a docs consistency check in CI (7.4) |
| Speed or scale claims with no method | Caddy "trillions", Hono "ultrafast", sturdyc "90%" | Every number links to its run record or is cut |
| Graphs with no table or axes | Ristretto, sturdyc | Table first, chart second, both from the same data file |
| Unbounded defaults presented without warning; management API with no guidance | cache-handler (unlimited body size, default API path) | Our defaults are bounded and the purge API is admin-only; say so, with the config names |
| Two repos where it is unclear which is stable | Souin / cache-handler | One canonical repo; the four modules and their versions in one table |
| README that is the whole manual (~100k characters) | sturdyc | README stays near 150 lines; depth lives on the site |
| Stale mirror README, no security policy | Varnish GitHub | Keep SECURITY.md accurate and visible |
| "Beta" badge never explained | Litestream | Every maturity label links to a definition |
| "RFC compliant" with nothing behind it | Souin, cache-handler | A conformance table with test IDs and the 81 expected failures listed |

### 3.3 In the middle (copy with care)

- Hono's one-command quickstart is excellent but stops without saying what you should see. Ours must show the `Cache-Status` output.
- Cloudflare's collapsing explanation ("the first request goes to origin, the other 999 wait") is clear, but the product page hides limits. Ours puts limits on the same page as the feature.
- Singleflight's page is minimal and honest, but silent on panics, error sharing and cancellation. This is Weir's best comparison point: say what Weir does in each case (FR-COA-*), with test names.
- Varnish's docs are accurate and stable across versions but hard to enter. Ours needs a front door, not just a manual.

### 3.4 Where Weir is distinct (the story the surfaces must carry)

No surveyed project combines, in one Go library with a written threat model: desynchronized expiry, cross-key stampede control, storage-outage degradation, and a cache key built as a security boundary. The honest version of this claim is narrower than the design doc's: each technique exists elsewhere; the combination, the threat model and the test evidence are the contribution. The site says that and no more.

## 4. Source of truth and drift control

The repo already has a strict hierarchy (docs/README.md, "Precedence"). The site and README are a new, lower layer. They never become normative.

Rules:

1. A fact has one home. The site links or generates; it does not restate a default by hand where a generated table is possible.
2. Generated from the repository at build time: the config reference (from `docs/01-technical-spec.md` §6 and `go doc`), the metrics table (from `observe/prom`), the Cache-Status values (from `cachestatus.go`), error and status mapping (runbook §6), threat table (docs/06), benchmark tables (a data file in `site/data/` that is itself copied from `docs/benchmarks.md` by a script and checked for equality in CI).
3. Hand-written: concept pages, guides, comparison, home, positioning. These cite requirement IDs (`FR-COA-4`) so a reader can find the normative text.
4. If writing a surface reveals that a normative document is wrong or two disagree, stop and report it. Do not fix the site around it. That is CLAUDE.md "When to stop and ask".
5. Anything touching a requirement, a default, a decision table or a public signature goes to Ashwin first. This plan changes none; section 9 lists the places where wording will need his call.

## 5. Surface inventory

"User-facing" means a person who is not a Weir contributor can reach it.

| Surface | Today | Action | Priority |
|---|---|---|---|
| `README.md` | 122 lines, strong content, but front-loads status and install before the "why"; settings table is 16 rows of dense prose; no demo, no non-goals, no output shown | Restructure to the outline in 6.1; move the settings table to the site and keep a three-row teaser | P0 |
| Website | none | Build per section 7 | P0 |
| `CHANGELOG.md` | accurate structure; stale GC figure (1.76 µs measured on the Linux box vs 1.08 µs on the reference machine); no mention of the hit-path work; "Valkey 7 or later" unsourced | Fix per section 9; add upgrade notes section template | P0 |
| `SECURITY.md` | good and short; says "latest v1.x" while only v0.1.0 exists; relies on private vulnerability reporting being enabled | Verify the setting is on; reword "Supported versions" once the tag lands; add a link to the site's security page | P0 |
| `docs/README.md` | index is accurate but says T-1 to T-44 (the model has T-45) and mixes reader types | Split into "Use Weir", "Operate Weir", "Understand Weir", "Contribute"; fix T-45; date line bump | P1 |
| `docs/runbook.md` | the most useful user document; 352 lines; 7.8 and 8.9 read as contradicting each other about `caddy validate` | Keep as the canonical operator doc; fix the contradiction; add a table of contents; site renders it | P1 |
| `docs/benchmarks.md` | contains superseded tables and "replace this table" notes next to the final ones | Rewrite as a results page with a "current" section and an "earlier runs" appendix; every absolute number states the machine | P1 |
| `docs/cache-tests-expected-failures.md` | good; says reasons were read, not each traced; pending re-run on the Mac | Keep; turn into the site's conformance page; add "last verified" date after the pending run | P1 |
| godoc (`doc.go` in each package, `Example` functions) | every exported identifier documented (enforced by `doccomments_test.go`) | Audit package overviews against the new message hierarchy; make sure pkg.go.dev front pages answer "what, install, first example" | P1 |
| Normative docs 00 to 10 | accurate, process-heavy, written for implementers | Do not rewrite. Link from the site as "Design documents". Fix the inconsistencies in section 9 | P2 |
| `PLAN-weir.md`, `CLAUDE.md`, `docs/cards/`, `docs/progress/` | internal process | Leave. Add a one-line note at the top of the site's contributor page that these exist | P3 |
| `docs/release.md` | maintainer procedure | Leave; add this plan's launch gate (section 10) | P2 |
| GitHub repo settings | unknown (verify) | Description, website URL, topics, social preview image, Discussions on or off, issue templates, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, release notes template | P1 |
| `pkg.go.dev` | derives from tags and godoc | Nothing to edit; fix by tagging and by package docs | P1 |
| Example binary `examples/weirproxy` | the only runnable example | Add a `README` in the directory and a Docker one-liner in the quickstart (if Ashwin wants a Dockerfile, see question Q6) | P2 |

## 6. Content specification

### 6.1 README outline (target 130 to 160 lines)

1. Title, one-sentence definition, the sentence-two fear answer.
2. Four badges: CI, pkg.go.dev, latest release, Apache-2.0.
3. "See it work": a 12-line Go program and a `curl -i` transcript showing `Cache-Status: Weir; hit; ttl=...` on the second call and `Weir; fwd=uri-miss; stored` on the first. Output must come from running `examples/weirproxy` (a script in `site/scripts/` regenerates it).
4. "What you get": six bullets, each with a requirement ID and a link to a site concept page: coalescing, jitter and early refresh, the limiter, SWR/SIE and the breaker, purges, the key as a security boundary.
5. "What Weir is not": CDN, WAF, rate limiter, private cache, application-data cache, 206 storage, prefix purge, multi-node memory store.
6. Install: the four modules with the same version, the xcaddy line, the Go 1.27 floor and the two-latest-releases policy.
7. Status and stability: the promise from today's README, trimmed, plus what is deferred (M16-02) and the experimental labels.
8. Security: one paragraph, links to the threat model summary page and `SECURITY.md`.
9. Performance: three numbers, each with machine, Go version and the command; link to the benchmarks page.
10. Compared with: a five-row table with "choose them when" entries; link to the full page.
11. Documentation, contributing, license.

Moves out of the README: the 16-row "Settings that take a protection back" table (becomes the site's "Tuning and trade-offs" page with the same rows, and the README keeps the rule in one sentence plus three examples), the strict forwarding essay (becomes a concept page; README keeps two sentences and the consequence list: key it, allow it, or lose it).

### 6.2 Site page inventory

Routes are relative to the site root. Every page has a unique `<title>` of two to five words plus site name, a one-sentence description, a canonical URL and an "edit on GitHub" link.

| Route | Purpose | Source | Notes |
|---|---|---|---|
| `/` | Convert a skimmer into a quickstart click | hand-written | hero: definition + code + `Cache-Status` output; failure-mode strip with five numbers; "what it does not do" strip; one primary button (Quickstart); one secondary (GitHub) |
| `/docs/quickstart/` | Five minutes to a hit | hand-written + tested snippets | tabs: Go (`weirhttp`), Caddy, the `weirproxy` binary. Each step shows expected output |
| `/docs/concepts/` index and children | Teach the model | hand-written | children below |
| `/docs/concepts/request-flow/` | The nine stages | docs/03 §1, condensed | diagram, see 7.5 |
| `/docs/concepts/cache-key/` | Why the key is a boundary | docs/02, 06 | strict forwarding, `Key.*`, `Forward.Allow`, cookies, the INV-1 sentence |
| `/docs/concepts/coalescing/` | Same-key herds | FR-COA | include leader aging and follower timeout |
| `/docs/concepts/freshness-and-staleness/` | TTL, jitter, early refresh, SWR, SIE, `Keep` | FR-FRS, FR-STL | worked timelines with numbers, as Varnish does |
| `/docs/concepts/origin-protection/` | Limiter, partitions, breaker, negative caching, miss-rate detector | FR-LIM, FR-CB, FR-NEG | one page, one diagram of the budget: `MaxConcurrent + MaxUpload` bounds origin concurrency |
| `/docs/concepts/purging/` | Soft, hard, groups, eager, epochs | FR-PRG | state "no prefix purge" |
| `/docs/concepts/stores/` | Memory (S3-FIFO) vs Valkey; one node vs many | docs/05 | the T-38 consequence for multi-node |
| `/docs/guides/net-http/` | Adapter details | `weirhttp` godoc | `Handler` vs `Middleware`; `TransportOrigin` and `HandlerOrigin` risks (T-4, T-45) |
| `/docs/guides/caddy/` | Caddyfile and JSON | docs/08, runbook 7 | directive table; admin API; build with xcaddy at one version; `caddy validate` caveat |
| `/docs/guides/production/` | Deployment checklist | runbook | `GOMEMLIMIT`, request body limits, timeouts, what to alert on |
| `/docs/guides/observability/` | Metrics, events, Cache-Status | docs/04 §9 | generated metrics table; example alerts; the `name` label under Caddy |
| `/docs/guides/incidents/` | Runbook steps | runbook | `SetMode` / admin `mode` with mandatory TTL |
| `/docs/guides/experiments/` | A/B bucketing in the key | docs/10 | label as v1 feature with the cookie and consent rules; mention open follow-up on cookie tossing |
| `/docs/reference/config/` | Every field, default, range | generated | search-filterable; security-relevant defaults flagged with the reason |
| `/docs/reference/cache-status/` | Header grammar and values | generated | |
| `/docs/reference/errors/` | Sentinels and status mapping | generated | 400/499/501/502/503/504 table |
| `/docs/reference/api/` | Link-out to pkg.go.dev per package | hand-written | no copy of godoc |
| `/docs/tuning/` | The settings that take a protection back | README table, expanded | each row: what you give up, when it is right, threat ID |
| `/security/` | Posture and reporting | docs/06, SECURITY.md | threat families, invariants, residual risks R-1 to R-8 in plain language, disclosure |
| `/security/threats/` | Table of T-1 to T-45 | generated | note ordering quirk in docs/06 |
| `/conformance/` | RFC 9111 and friends | cache-tests baseline | 365 tests, 284 pass, 81 expected failures, each with its reason and decision ID |
| `/benchmarks/` | Honest numbers | data file | section 6.3 |
| `/compare/` | Honest comparison | hand-written, dated | section 6.4 |
| `/changelog/` | Release notes | `CHANGELOG.md` | rendered, with the versioning and support policy on top |
| `/about/` | Origin of the project and naming | seed doc 0.1 and 2.1 | short; the original three weaknesses from the design doc are a good story |
| `/404` | | | links to search and quickstart |
| `/llms.txt`, `/sitemap.xml`, `/robots.txt`, `/feed.xml` (changelog) | machine surfaces | generated | Ashwin to confirm `llms.txt` (Q9) |

### 6.3 Benchmarks page rules

The repo has good data and a messy file. The page must:

- State the machine for every absolute number. Quote the hit path as "about 0.8 µs per cached hit in-process on an Apple M4 Pro with Go 1.27.1, excluding net/http and TLS" (833.5 and 821.4 ns/op, 12 allocs/op, `-count 10`, idle machine). Never quote an absolute figure as a property of Weir on other hardware.
- Show the 4-core Linux container numbers separately and say they drift.
- Report the five load scenarios as a table: steady hits (438k req/s, p99 about 18 to 22 µs), synchronized expiry (10,000 keys, origin peak in flight 32), busting flood (flood capped at 8 in flight, normal p99 36.9 µs alone vs 41.0 µs under flood), origin brownout (every request 2xx, 52,975 stale responses), store flap at 50% (0 client errors).
- Report GC at 1M entries as 1.08 µs per request on the reference machine, 1.76 µs on the Linux box, and the deferred M16-02 decision, without spin.
- Show the commands: `make bench`, `make load`.
- End with "What this does not show": no comparison against other caches, no network or TLS, no real origin, one reference machine.
- Do not publish any head-to-head number against another product. We have not run one (Q3 answered "honest comparison", which this plan reads as qualitative only).

### 6.4 Comparison page rules

The comparison is qualitative and dated ("checked 2026-10-xx against version N"). Each row has: what it is, what it does that Weir does not, what Weir does that it does not, "choose it when", "choose Weir when".

Products and the claim that must be verified against that product's own docs before it is written:

| Product | Claim to verify first |
|---|---|
| Varnish | coalescing and grace exist and are mature; multi-key stampede control is not a built-in budget (the Varnish grace docs say coalescing handles same-key only; confirm current text); VCL gives programmability Weir lacks |
| nginx `proxy_cache` | `proxy_cache_lock`, `proxy_cache_use_stale updating`, no jitter, no breaker |
| Souin / Caddy cache-handler | RFC coverage, surrogate keys, many backends; no TTL jitter and no key-hardening story as of the cited README (re-check; the survey found README contradictions about storage backends) |
| Pingora and `pingora-cache` | library in Rust, cache module marked experimental; Weir is Go and a policy layer |
| `x/sync/singleflight` and sturdyc | same-key only; what Weir adds; what they are better at (generic function caching, no HTTP semantics) |
| CDN request collapsing (Cloudflare, Fastly) | managed service vs library; cost and control |

If a claim cannot be verified from the product's own documentation, it is left out, not softened. The page ends with "Where we are wrong or unproven" (one pointer to the known limits).

### 6.5 Claims register

Create `site/content/claims.md` (not published) with every quantitative or comparative claim on the site, the source file or run record, the date checked, and the version it was checked against. CI fails if a page contains a number matching the pattern of a benchmark figure that is not listed in the register. This is the guard against the "unsourced speed claim" anti-pattern.

### 6.6 Tone

Plain, specific, restrained. Short sentences mixed with longer ones. Numbers and names instead of adjectives. No "robust", "seamless", "leverage", "comprehensive", "delve", no rule-of-three reflex, no "not just X but Y", no em dashes. State limits before being asked. Address the reader as "you". Name risks with their IDs.

## 7. Site architecture

### 7.1 Stack

- Next.js (App Router) with `output: 'export'`, so the result is static files. No server runtime, no edge functions, no ISR. Reasons: the content is static, there is nothing to personalize, and a static site is the cheapest and most reliable thing to host on Cloudflare.
- TypeScript, strict. React Server Components for content pages; client components only for the theme toggle, copy buttons, tabs, search UI and the config-table filter.
- MDX for hand-written pages via `@next/mdx` (or `next-mdx-remote` if the MDX files live outside `app/`; pick one in the first slice and record it).
- Styling: plain CSS with custom properties (tokens), CSS modules per component. No Tailwind unless Ashwin wants it (Q5): the site is small and a token file is easier to review.
- Syntax highlighting at build time with Shiki (no client JS). Go, Caddyfile, JSON, shell, diff.
- Search: Pagefind, run after `next build`, index the static output. Zero server.
- Diagrams: hand-authored inline SVG, accessible (title, desc, text fallbacks); no runtime diagram library.
- Charts: only on the benchmarks page; static SVG generated at build from the same data file as the tables.
- Package manager: pnpm, lockfile committed, Node version pinned in `.nvmrc` and `package.json` `engines`.
- Dependencies are minimal and pinned; run `pnpm audit` in CI. Dependencies belong to `/site/package.json` only; nothing is added to any `go.mod`.

### 7.2 Repository layout

```
site/
  package.json  pnpm-lock.yaml  .nvmrc  next.config.ts  tsconfig.json
  app/                    # routes (see 6.2)
  components/             # Callout, CodeTabs, Cmd, CacheStatusExplainer, Timeline, DataTable, ...
  content/                # hand-written MDX
  data/                   # benchmarks.json, conformance.json, release.ts (RELEASED flag, versions)
  generated/              # build-time output of scripts/gen-*.ts (gitignored; built in CI)
  scripts/                # gen-config, gen-metrics, gen-threats, gen-conformance, snippets, check-claims
  public/                 # favicon, og images, fonts (self-hosted)
  styles/                 # tokens.css, base.css
  README.md               # how to build, edit, release
```

`site/` is excluded from Go tooling by having no `.go` files and no `go.mod`. Verify that `go.work`, `make check`, `deps_test.go` and the `trace` script do not walk it (Q: the trace script scans for requirement IDs; confirm it ignores `node_modules`). Add `site/node_modules` and `site/.next` to `.gitignore`.

### 7.3 Cloudflare deployment

- Target: Cloudflare, static assets from `site/out`. Cloudflare's current guidance for static sites and its product naming (Pages vs Workers static assets) changes; at implementation time read the current Cloudflare docs and pick the supported path. Record the choice in `site/README.md`. Verify, do not assume.
- Custom domain `weir.ashwinsathian.com`: a CNAME to the Cloudflare target, set up by Ashwin in his DNS (he owns `ashwinsathian.com`; Claude cannot do this). TLS via Cloudflare.
- Headers (`_headers` or the platform equivalent): `Content-Security-Policy` with no inline script (hash or nonce-free; Next static export emits inline bootstrap scripts, so either allow their hashes at build or use the Next option for strict CSP; test in a browser), `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `Permissions-Policy` minimal, long immutable cache on `_next/static`, short cache on HTML.
- Redirects: `/docs` to `/docs/quickstart/`; `/github` to the repo; versioned doc paths reserved (`/docs/v1/...`) so a v2 does not break links (decide in Q8 whether to ship `/v1/` prefixes now).
- Preview deploys for every PR (platform feature); production deploys only from `main`.
- Analytics: none by default (Q7). If wanted, Cloudflare Web Analytics (cookieless). No third-party scripts, no fonts from third-party CDNs.

### 7.4 CI for the site

A separate workflow `.github/workflows/site.yml`, path-filtered to `site/**`, `docs/**`, `README.md`, `CHANGELOG.md`:

1. `pnpm install --frozen-lockfile`, `pnpm audit --prod`.
2. Type check, lint, format check.
3. Generators run; `git diff --exit-code` on checked-in data files (benchmarks.json) to prove it matches `docs/benchmarks.md`.
4. Snippet check: every Go code block marked `// from: <file>#<Example>` is byte-equal to the source; Caddyfile blocks are parsed by `caddy adapt` in a job that builds Caddy (reuse the `caddy-build` job's output).
5. Link check on the built site (internal links and anchors; external links only on a nightly job to avoid flaky PR failures).
6. Build; a11y check with axe against a fixed list of routes; Lighthouse CI budgets: performance 95+, accessibility 100, best practices 95+, SEO 100 on `/`, `/docs/quickstart/`, `/benchmarks/`.
7. Claims register check (6.5).
8. Style lint: a small script that fails on em dashes and the banned words from 6.6 in site content and README.
9. Release-state check: if `RELEASED` is true in `site/data/release.ts`, a job verifies `go list -m github.com/AshwinSathian/weir@<version>` and the three sub-modules resolve from the module proxy. This is the launch gate's automation (section 10).

Existing `make check` is untouched. Add `make site-check` as a convenience wrapper; do not add it to `make check`, because that would force Node on every contributor.

### 7.5 Design system

Keep it small and calm; the project's voice is engineering, not marketing.

- Tokens on `:root` for color, spacing (4 px scale), type scale, radii, shadows; redefined under `prefers-color-scheme: dark`, with an explicit manual override stored in `localStorage` guarded by try/catch. Body has an explicit background. No flash of wrong theme: set the attribute in a tiny blocking inline script (hashed in the CSP) or use CSS-only `prefers-color-scheme` plus override via class.
- Palette: a water-adjacent neutral base (slate), one accent (a teal or blue with at least 4.5:1 on both backgrounds), a distinct warning color and a danger color for the "takes a protection back" callouts. Verify contrast ratios with a script, not by eye.
- Type: system font stack for body to avoid font cost, one self-hosted monospace (variable, subsetted) for code. If a display face is wanted, self-host one weight.
- Layout: single column up to 720 px for prose; docs pages use a three-zone layout (nav, content, on-this-page) above 1100 px and collapse to a drawer below. 16 px side gutter at phone width; no horizontal page scroll; code blocks scroll inside their own box, with a copy button.
- Components: `Callout` (note, caution, protection-removed), `CodeTabs` (Go / Caddyfile / JSON), `Cmd` (shell command with copy), `CacheStatusExplainer` (hover or tap a header value to see what each parameter means), `Timeline` (SVG for SWR / SIE / jitter, built from numbers), `ConfigTable` (filterable), `IdBadge` (requirement and threat IDs link to the right doc section), `Matrix` (comparison).
- Interaction: keyboard reachable everywhere, visible focus, skip link, `prefers-reduced-motion` respected, no autoplay. The hero demo is a pre-recorded transcript rendered as text (not a video) with a "replay" control, so it is accessible and copyable.
- Imagery: one OG image template per page type, generated at build; a simple logotype (a low dam line mark) drawn as SVG. No stock photos.

### 7.6 Home page composition

1. Header: logotype, Docs, Concepts, Security, Compare, Benchmarks, Changelog, GitHub (with star count omitted, to avoid third-party calls).
2. Hero: definition, fear-answer, `go get` line, primary and secondary buttons.
3. Demo: the stampede transcript (1,000 concurrent requests for one cold key, 1 origin call; 5,000 distinct keys under a limit of 64, peak in flight stays at the cap). Numbers come from the repo's own tests (T6.2 and T6.3 test names linked), not invented. If the exact figures are not in a test, show the test's assertion instead.
4. Five failure modes, each a short card: synchronized expiry, hot-key stampede, cross-key stampede, storage or origin outage, cache poisoning and busting. Each card names the feature and the requirement ID.
5. A 10-line Go example and a 6-line Caddyfile, side by side.
6. "What it does not do".
7. Evidence strip: 284 of 365 cache-tests passing with 81 explained; 29 fuzz targets run for an hour each before release; 100 repeats under `-race`; standard-library-only root module. Each links to its page. Phrase them as facts about the release gate, with the run link.
8. Footer: license, security policy, changelog, "edit this page".

## 8. Work packages for implementation

Each package is one Claude Code session, one PR, in this order. The repo's card system (docs/cards/README.md) applies if Ashwin wants these as cards; if so, create them as `S-01` to `S-10` and have `/handoff` mark them. Sizes follow the card sizing guide; if a package overruns, split it (CLAUDE.md overrun rule).

| ID | Title | Deliverable | Acceptance |
|---|---|---|---|
| S-01 | Truth pass on existing docs | Fix every item in section 9; bump date lines | No remaining item in 9; `make check` green; no behavior text changed beyond wording |
| S-02 | Site skeleton and CI | `site/` scaffold, tokens, layout, theme, `site.yml`, `make site-check`, empty route stubs for 6.2 | `pnpm build` produces `out/`; Lighthouse and axe pass on stubs; CSP tested in a browser |
| S-03 | Generators | `gen-config`, `gen-metrics`, `gen-threats`, `gen-conformance`, snippet extractor, claims checker | Generated pages match source; CI diff check works; intentionally break a source to see the check fail |
| S-04 | Quickstart, concepts, guides | MDX content for `/docs/**` | Each page cites requirement IDs; every snippet is tested or extracted; style lint passes |
| S-05 | Reference and security pages | Config, Cache-Status, errors, threats, residual risks, disclosure | Generated tables complete; plain-language residual risks reviewed by Ashwin |
| S-06 | Benchmarks and conformance | Data file, tables, SVG charts, "what this does not show"; conformance table with 81 reasons | Numbers equal to `docs/benchmarks.md`; claims register complete |
| S-07 | Home, about, changelog, 404, SEO | Hero demo, evidence strip, sitemap, OG images, `llms.txt` (if Q9), feed | Lighthouse budgets met on `/`; social previews checked in two validators by Ashwin |
| S-08 | Comparison page | `/compare/` with verified, dated claims | Every cell has a source URL and date in the claims register; Ashwin approves wording |
| S-09 | README, CHANGELOG, SECURITY, docs index restructure | New README per 6.1; docs/README split by reader; repo metadata checklist | README at 130 to 160 lines; links resolve; `make check` green |
| S-10 | Launch | Domain wiring, deploy, redirects, post-launch checks, release-state flip | Launch gate (section 10) passes; checks in 10.3 done |

S-01 comes first because every later slice would otherwise copy a stale fact. S-09 comes late so the README can link to live pages, but its non-link edits (structure) can land earlier if Ashwin prefers.

### 8.1 Definition of done for every slice

Tests or checks named in the slice pass; style lint passes; no number without a register entry; every new page has title, description, canonical, breadcrumbs, and "edit on GitHub"; keyboard and screen-reader pass on the changed routes; phone width checked at 360 px and 390 px; both color schemes checked.

### 8.2 What not to build

Blog, newsletter, comment system, account system, cookie banner (no cookies), chat widget, A/B tests on the marketing site, a docs version switcher (until a v2 exists), a Weir "playground" in the browser (tempting, large, and it would execute untrusted-looking input in a security-themed project).

### 8.3 Verification the survey could not do

The sandbox could not reach most marketing sites. Before S-02 sets the visual direction, Ashwin or an agent with a browser should look at three to five of these on a phone and a desktop: Caddy, Litestream, Hono, Bubble Tea or charm.sh, Varnish docs, Envoy docs, Tailscale. Record, for each: above-the-fold message, navigation depth, mobile behavior, contrast, search. Those notes go in `site/README.md` under "design references". The plan does not depend on their outcome; they tune the look.

## 9. Truth pass: inconsistencies to fix in S-01

Each item is a wording or date fix. Any that would change a requirement or default is marked "ask".

| # | Where | Problem | Fix |
|---|---|---|---|
| 1 | `README.md`, `docs/runbook.md`, `docs/08`, `caddy/doc.go` | `@v1.0.0` installs shown as available; no `v1.0.0` tag exists and sub-modules use `replace` and `v0.0.0` | No text change now (the user asked for copy as if released). Gate: section 10 |
| 2 | `CHANGELOG.md:34` | GC cost quoted from the Linux box (1.76 µs); the reference machine measured 1.08 µs | Quote both with machines |
| 3 | `CHANGELOG.md` | Hit-path optimization (about 0.83 µs, 12 allocs/op, 16-alloc budget) not mentioned | Add under the engine heading |
| 4 | `CHANGELOG.md:22` | "Valkey 7 or later" not backed by a source; CI uses Valkey 8.1, one log mentions Redis 7.0.15 | Ask Ashwin which versions are tested; state only those |
| 5 | `SECURITY.md:15` | "latest v1.x" while only `v0.1.0` exists | Fine at release; verify private vulnerability reporting is enabled on the repo |
| 6 | `docs/README.md:13` | "T-1 to T-44"; the model has T-45 | Fix, bump date |
| 7 | `docs/01-technical-spec.md:3` | "approved for Phase 0 and Phase 1" while it covers later phases | Reword the status line (ask: it is normative) |
| 8 | `docs/01 §1.2` | "arrive in M12" for targeted cache-control, now implemented | Past tense |
| 9 | `PLAN-weir.md:214` | "v1.0.0 at the end of Phase 1" vs D24 amended | Align with D24 as amended |
| 10 | `docs/03-hld.md` | Dated 2026-09-27; omits `SetMode`, upload pool, streams, upgrade routing, experiments, snapshots, Valkey | Add an "Updated since" note listing the gaps, or update the sections (larger; may be its own card) |
| 11 | `docs/benchmarks.md` | Header date older than contents; superseded tables and "replace this table" notes remain; 4 µs "provisional" ceiling vs current 1.25 µs | Restructure as in the inventory |
| 12 | `docs/runbook.md` 7.8 vs 8.9 | Disagree about `caddy validate` and the store | State once: validate provisions the store and may write a key-generation epoch; connection is lazy |
| 13 | `README.md:118` | "Go 1.27 or later" vs "two latest releases" | Say: minimum Go 1.27.0; CI tests the two latest releases |
| 14 | `docs/cache-tests-expected-failures.md` | `make cache-tests` re-run pending on the Mac | Add "last verified" after the run |
| 15 | All | "weir/caddy" vs package `weircaddy` | Standardize per section 2 |
| 16 | `README.md:9-10` vs `CHANGELOG.md:5` | README present tense, CHANGELOG "unreleased" | Resolved by the launch gate |
| 17 | `README.md` Stability | Promise "from v1.0.0" while untagged | Same as 1 |
| 18 | `docs/06` | T-45 appears out of numeric order | Note it on the site page, or reorder (editing a normative doc: ask) |

Open behavior follow-ups from the last review (If-Match / If-Unmodified-Since ignored on hits; unkeyed `Pragma` and `Cache-Control` responses stored under the shared key; `QuerySort` reordering repeated names; `Entry.Size` undercount; Valkey record size limit; experiment cookie tossing) are decisions for Ashwin before the tag. Each that stays unfixed must appear on the site's "Known limits" section with its ID. The surfaces must not claim stronger behavior than the code has on the day of launch.

## 10. Launch gate

The decision to write as if `v1.0.0` is out is reasonable for copy, but it makes every install line false until the tags exist. These checks make the site safe to publish.

### 10.1 Before the production domain serves the site

1. All four tags exist and resolve from the proxy: `weir@v1.0.0`, `observe/prom/v1.0.0`, `store/valkey/v1.0.0`, `caddy/v1.0.0`. The `docs/release.md` procedure, including the `v1.0.0-rc.1` rehearsal, is complete.
2. `make release-check` passes on the release branch.
3. The smoke test from `docs/release.md` step 9 passes (xcaddy build, `caddy list-modules | grep weir`).
4. The pending items in STATUS are closed: `make vuln`, `make cache-tests`, `make check` on the Mac, and the behavior decisions.
5. `CHANGELOG.md` has the date instead of "(unreleased)".

### 10.2 If the tag slips

Preview deploys on Cloudflare stay available for review (they are not the public domain). Do not point `weir.ashwinsathian.com` at production until 10.1 holds. If Ashwin wants the domain live earlier, flip `RELEASED` to false: the install blocks show `go get github.com/AshwinSathian/weir@main` (root only; the sub-modules cannot be fetched by outsiders until tagged), and a pre-release banner appears. One switch, no copy edits. Build and test both states in CI.

### 10.3 After launch

Submit the sitemap to search consoles; check the pkg.go.dev pages populate; run the link checker externally; confirm social preview on two platforms; set the GitHub repo's website field and topics (`go`, `http-cache`, `caddy`, `stale-while-revalidate`, `rfc-9111`, `cache-stampede`); post nothing until the quickstart has been followed by someone who did not write it.

## 11. Measures of success

| Measure | Target | How |
|---|---|---|
| Time from landing to a working hit | under 5 minutes for someone with Go installed | One outside tester, timed |
| README length | 130 to 160 lines | `wc -l` |
| Unsourced numbers on the site | 0 | claims register check |
| Contradictions between README, site and normative docs | 0 known | generators plus review |
| Lighthouse | 95/100/95/100 on three routes | CI |
| Broken internal links | 0 | CI |
| Pages that name a limit next to the feature | all concept pages | checklist in review |

## 12. Adversarial review of this plan

The plan was attacked from six angles. Each finding states the attack, whether it held, and what changed. Findings marked "open" need Ashwin.

### 12.1 Honesty and trust

- **Attack:** "Write as if v1.0.0 is out" will make `go get ...@v1.0.0` fail for every early visitor, and an unconditional compare page invites a fact-check by Souin's or Varnish's maintainers. **Held.** Added the launch gate (section 10) with CI automation and a one-flag fallback, made the compare page qualitative, dated and sourced per cell (6.4), and added the claims register (6.5).
- **Attack:** The design doc's claim that nothing else combines these features is the kind of sentence that ages badly. **Held.** Section 3.4 narrows it to "the combination, the threat model and the test evidence"; the site never says "the only".
- **Attack:** Benchmark numbers from one laptop will be quoted out of context. **Held.** 6.3 forbids absolute figures without machine, separates the Linux container, and requires the "does not show" paragraph.
- **Attack:** Showing 81 cache-tests failures looks bad. **Did not hold as a reason to hide them.** Every failure maps to a decision ID; a conformance page that lists them is the thing competitors lack.
- **Open:** Residual behavior follow-ups (If-Match and others) are still unresolved. If any stays, the site lists it under known limits. Ashwin decides which.

### 12.2 Drift and maintenance

- **Attack:** The site becomes a third copy of the truth and rots. **Held.** Section 4 rules and generators in S-03; CI diff checks; the claims register.
- **Attack:** Generators that parse markdown tables break silently when the doc layout changes. **Held.** The generators fail loudly on unexpected structure and have a deliberate-break check in S-03's acceptance.
- **Attack:** Snippets in prose go stale. **Held.** Snippets are extracted from tested `Example` functions or built with `caddy adapt` in CI.
- **Attack:** A one-person project cannot maintain 30 pages. **Partly held.** Page count is justified by a reader need each, but 6.2 marks the minimum viable launch set: `/`, quickstart, request flow, cache key, freshness, origin protection, caddy guide, production guide, config reference, security, conformance, benchmarks, compare, changelog. Everything else may ship after launch. Added to S-04, S-05 as an ordering rule.

### 12.3 Security and the project's own rules

- **Attack:** A Next.js toolchain in a repo whose rule 1 forbids dependencies outside stdlib. **Did not hold, with a guard.** Rule 1 concerns Go modules. The risks are accidental coupling: `go.work`, `deps_test.go`, trace scripts walking `node_modules`. Section 7.2 lists the checks; `site/` has no `.go` files.
- **Attack:** npm supply chain on a project that sells security. **Held.** Few dependencies, frozen lockfile, `pnpm audit`, no third-party scripts at runtime, Dependabot or Renovate scoped to `/site` (add to S-02), build reproducibility noted in `site/README.md`.
- **Attack:** Strict CSP and Next's inline bootstrap scripts conflict. **Held.** Called out in 7.3; the slice must test in a real browser, not trust config.
- **Attack:** The admin purge API documentation could encourage exposing it. **Held.** Guide text must say purge exists only on the Caddy admin endpoint and show the safe setup first; no `curl` example against a public hostname.
- **Attack:** The experiments page documents cookie signing and could mislead on cookie tossing. **Held.** It carries the open follow-up as a stated limit.
- **Attack:** CLAUDE.md says one session per card and docs overrides. Work outside cards breaks the protocol. **Held.** Section 8 offers `S-` cards; if used, create them via the card format; otherwise the repo's rule for doc-only work applies.

### 12.4 Product and audience

- **Attack:** The site is written for Go library users, but the Caddy path may be the larger audience. **Held.** Quickstart tabs put Caddy and Go on equal footing; the home page shows both.
- **Attack:** Competitor-style comparisons assume the reader already knows alternatives. **Partly held.** `/docs/concepts/` starts from the failure modes, not from products.
- **Attack:** No evidence of real users. **True and cannot be fixed by writing.** The site avoids "used by" claims. Add a "Using Weir? Tell us" prompt only if Ashwin wants it (Q10).
- **Attack:** An "About" page with the origin story reveals a former employer's engine. **Held.** The design doc describes it; the about page uses only the three public weaknesses, without naming the company or its product. Ashwin approves wording.

### 12.5 Technical choices

- **Attack:** Next.js is heavy for a static documentation site; Astro or plain HTML would ship less JS. **Held, but the choice is Ashwin's.** Mitigations: static export, Server Components by default, JS budget (home under 100 kB gzipped total JS, docs pages under 150 kB), Lighthouse gate. If the budget cannot be met, the slice reports it rather than raising the budget.
- **Attack:** Cloudflare's static hosting product naming and limits change; the plan could name the wrong one. **Held.** 7.3 says to read current docs at implementation time and record the choice.
- **Attack:** Pagefind adds a build step and a binary. **Accepted.** It is the lightest search that needs no server; fallback is no search at launch and a link to GitHub search.
- **Attack:** Light/dark theme without flash needs inline script, conflicting with CSP. **Held.** 7.5 offers a CSS-first approach and a hashed script; test both.
- **Attack:** Generated OG images need fonts and a headless renderer. **Held.** Use build-time SVG-to-PNG with an embedded font, or ship static per-section images; decided in S-07.

### 12.6 Process and scope

- **Attack:** This plan itself is a document in `docs/` numbered like a normative one. **Held.** The header says it is a plan and changes no requirement. It is not added to the precedence order. Consider moving it to `docs/progress/` or deleting after the work; Ashwin decides (Q11).
- **Attack:** Ten slices is a lot; value arrives late. **Partly held.** Order delivers the truth pass first (immediate value), then a deployable skeleton, and S-10 can launch with the minimum set from 12.2.
- **Attack:** The survey had gaps (most marketing sites unreachable). **True.** Stated in sections 3 and 8.3; the plan's structure relies on READMEs and docs that were read, and design direction waits on a human look.
- **Attack:** No outside testing before launch. **Held.** S-10 requires one outside tester on the quickstart.

## 13. Questions for Ashwin

Each has a default; implementation proceeds with the default unless told otherwise.

| # | Question | Default |
|---|---|---|
| Q1 | Should S-01 edit normative documents' wording (items 7, 9, 18), or only list them for you? | Edit non-normative docs; list normative ones for your approval |
| Q2 | Which Valkey versions are actually tested (CHANGELOG says "7 or later")? | State only Valkey 8.1 until told otherwise |
| Q3 | Is a qualitative-only comparison acceptable, with no head-to-head benchmarks until you run them? | Yes |
| Q4 | Are the behavior follow-ups (If-Match, unkeyed Pragma/Cache-Control storage, QuerySort order, `Entry.Size`, Valkey record limit, cookie tossing) fixed before launch or listed as known limits? | Listed as known limits with IDs |
| Q5 | Tailwind or plain CSS tokens? | Plain CSS tokens |
| Q6 | Should the repo ship a Dockerfile for `examples/weirproxy` for the quickstart? | No; `go run` only |
| Q7 | Analytics: none, or Cloudflare Web Analytics? | None |
| Q8 | Ship `/v1/` path prefixes for docs now? | No; add when a v2 is planned, keep a redirect map ready |
| Q9 | Publish `llms.txt`? | Yes, generated from the page index; low cost |
| Q10 | Include a "Using Weir? Tell us" prompt or a user list? | No |
| Q11 | After implementation, keep this file in `docs/`, move it to `docs/progress/`, or delete it? | Move to `docs/progress/` |
| Q12 | Logo and naming: do you have a mark, or should S-02 draw a simple SVG dam-line logotype? | Draw a simple one; you replace it if you have one |
| Q13 | Do you want the site content (MDX) covered by the Apache-2.0 license or a separate documentation license? | Apache-2.0, stated in the footer |
| Q14 | Who is the named maintainer/author on the About page, and is the "origin story" in section 6.2 acceptable to publish? | Your name, three public weaknesses only, no employer |
| Q15 | Is the repository public with private vulnerability reporting enabled? | Verify before launch; SECURITY.md depends on it |
