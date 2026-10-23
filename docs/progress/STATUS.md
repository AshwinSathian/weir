# Status

Updated: 2026-10-23
Phase: v1.2.0 released 2026-10-23 (tags on `release/v1.2.0`; CHANGELOG dated 2026-10-22)
Current card: none
Card state: ready
Branch: none
PR: post-release updates (version pins, release log) and the round 3 results (benchmarks, comparison, site) are open
Next card: none open. Next steps: merge the two open pull requests, deploy the site, then the Dependabot updates (#137 CI actions, #138 Go dependencies), held back during the release.

## Waiting on Ashwin

- None.

## Notes for the next session

Release
- Release from a separate worktree (docs/release.md). Never ask the module proxy for a version before its tag is pushed, and keep `set -o pipefail` around `release-pin.sh`.
- `make release-check` fails on `main` by design: the sub-modules require `weir v0.0.0` with `replace` there, and the pinned `go.mod` files live only on a release branch.
- Toolchain: Go 1.27.3 or later (`GOTOOLCHAIN=go1.27.3`) for `make vuln` and releases; 1.26.10 or later on Go 1.26.
- Before the tag: `make load` and `benchstat` on an idle machine, `make cache-tests`, the `release-gate` workflow on the release candidate commit. Run load and benchmarks in the foreground (a background process runs at a fraction of its speed on macOS).

Testing
- `make check` does not run the `integration` or `load` tagged tests. A change to the limiter, the breaker or a store needs `make test-valkey` against a local Valkey (with a storm server and a replica started with `--enable-debug-command yes`, see the Makefile) and the affected `loadtest` scenarios.
- Lint: `GOBIN=$HOME/go/lintbin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0`, then `make check GOLANGCI=$HOME/go/lintbin/golangci-lint`.
- The caddytest end-to-end tests listen on 127.0.0.1:9080 and :2999: two `make check` runs at once on one machine make them fail.

Comparison harness (bench/compare)
- Round 3 (2026-10-22, Weir v1.2.0) ran every pillar in one invocation per script on a quiet machine. The speed pillar's timing rows all failed the drift gate: the control's p50 held within 5%, its p99 moved 12 to 20% inside every repetition. Only its counts (memory per object, hit ratio, origin calls) are published, by the owner's decision. Publishing latency needs longer timed runs or a p99 rule that fits runs of this length; either is a docs/11 §6 change for the owner.
- `run.sh` (counting pillars, about two and a half hours in full; one call per subject and tier keeps each under ten minutes), `probes.sh`, `conformance.sh` and `speed.sh` (speed and memory, about two hours, needs a quiet machine and Docker Desktop with 8 CPUs).
- Unreleased engine changes are measured with the `-next` subjects (`SUBJECTS=caddy-weir-next`), built from the working tree.
- Charts on the site are drawn from `docs/benchmarks.md` by `site/lib/results.ts`. A table whose columns change, or a rerun that contradicts a headline tile, fails `make site-check` on purpose: update `HEAD` in that file or the tile copy in `site/components/charts.tsx`.

Site
- `make site-check` (Node 24, pnpm) is the gate; `make site-deploy BRANCH=main` uploads with wrangler. `site/README.md` explains the include directives and the claims register. Requirement IDs are removed from the site's prose at render time (`site/lib/md.ts`).
- Caddy's admin endpoint refuses requests that carry browser fetch metadata, so hand checks against `:2019` need a plain client.

Workflow
- Use `git worktree add` for any work while another session may be using the main checkout.
- `caddy validate` against a Valkey store can spend the key-generation purge; Caddy gives modules no way to detect validate (runbook 7.8, Caddy guide).
