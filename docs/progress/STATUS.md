# Status

Updated: 2026-10-10
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: claude/funny-bohr-vcgmor
PR: https://github.com/AshwinSathian/weir/pull/78
Next card: P2-03b (End-to-end scenarios under caddytest)

## Waiting on Ashwin

none. Decided 2026-10-10 (P2-02): multi-host is an explicit `multi_host` key; scanning the http app was rejected (handler cannot find its own route; global scan would cap unrelated sites and flush their stores). Documented in 08 §2/§3/§4b.

## Decided 2026-10-09 (M16-01)

Ashwin delegated "take decisions on all items" on PR 72. Approved the recommendation: keep the heap layout; D36 now gates on GC µs per request (at most 2 µs at 1M entries, 1 KiB, default `GOGC`; now 1.76), reported by `TestGCAt1MEntries`, not asserted; `GOGC=200` is the documented lever. M16-02 deferred (moved to `docs/cards/20-later.md`, reopens as a design change if the gate is exceeded or more than 1M entries are needed). M16-01 marked done.

## Decided 2026-10-09 (M15-01)

From the adversarial review of PR #71 (Ashwin delegated "take decisions on all items"). "Waiting on Ashwin" was empty. No must-fix.

- Scrub failures do not count toward the store breaker (same rule as `purgeEpoch`), emit `EvStoreError{scrub}`; pinned by `TestStoreGuardScrubFailureNotCounted`. LLD 5.2 and the event table updated.
- Scrub ignores request times and cannot stop an in-flight store: documented in LLD 13.5 and 05 §5.3 rather than changing `Scrubber`'s signature (public API; the epoch decides reachability). A part-way epoch failure scrubs nothing (documented; repeating is safe).
- Added engine tests by URI and group tag with a surviving URL, a cancelled-context case, and a concurrent Scrub/Set/Get/Delete race test. `Scrubber` doc comment and docs/07 row updated.
- Declined: keeping `context.Canceled` in the scrub error (nit; callers see `store.ErrUnavailable` and their own ctx).

## Blockers

none. golangci-lint in this container is built with Go 1.25 and cannot load the Go 1.27 config; run it with `GOTOOLCHAIN=go1.27.0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`. CI must confirm.

## Decided 2026-10-09 (P2-00, review of PR 73)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR. Decisions, all written into 08:

- Admin routers are rebuilt on every load (my earlier claim that they survive reloads was wrong). The registry stays but holds a set of live engines per name, removed by identity, so a failed load keeps the serving engine reachable. `mode` applies to all live engines of a name, `stats` returns an array, `purge` goes through any one. Admin bodies are bounded (1 MiB, 1000 URLs, 100 groups).
- Same `name` with different settings fails `Provision` within one load only; across loads (resize reload) it is allowed.
- Store-level settings are fixed at build. Pool key adds an "owner cap on" boolean: one host to two starts a new store once, so the FR-FAIR-3 cap is never silently missing. Auto-sized stores keep their size; no cross-load 40% guarantee (store has no resize), warning plus explicit `max_bytes` advice.
- OQ-C1 amended: a key-generation hash change writes a hard epoch, not soft (soft entries stay servable in SWR and stale-if-error windows, FR-PRG-2). This changes a decision from 2026-09-27; PLAN 2.2 reworded.
- Per-client placeholders (`{remote_host}` and header placeholders) on handlers after `weir` are an unkeyed input like X-Forwarded-For; 08 §6 and P2-03/P2-07 cover it. Follow-up: add to 06 T-45/R-6 at its next revision.
- Snapshots: the superseded store skips its snapshot; `name` is restricted to `[A-Za-z0-9._-]{1,64}`; config keys table added to 08 §2.
- Cards: P2-01 split into P2-01, P2-01b (Caddyfile), P2-01c (xcaddy CI); `TestRetryAfterSurvivesHandleErrors` moved to P2-03b (needs caddytest).

## Decided 2026-10-09 (P2-01, review of PR 74)

Ashwin delegated "take decisions on all items"; "Waiting on Ashwin" was empty. An independent agent attacked the PR; no must-fix. Decisions:

- `name` may not start with a dot (admin URL `/weir/../purge` collapses). Changes the 08 §2 charset rule; 08 updated.
- `max_bytes` is capped at 1 PiB at parse time (later budget sums cannot overflow); `Provision` warns when `max_bytes` or `snapshot_dir` is set, since P2-02 applies them.
- A second `Provision` on a provisioned `Handler` fails instead of leaking the first engine. Byte sizes accept digits and one decimal point only; JSON `null` is a no-op; doubled `weir:` error prefix removed.
- Risk for P2-01c: `caddy/go.mod` uses `replace => ..`, which importers ignore; `xcaddy build --with .../weir/caddy` outside the repo may not resolve the root module until it has a tag. P2-01c must test this.
- P2-03: `Cleanup` clears `h.engine` without a lock; `ServeHTTP` must read it safely (atomic pointer or the engine registry).

## Decided 2026-10-10 (P2-01b, adversarial review of PR 75)

"Waiting on Ashwin" was empty. No must-fix. Decided: sub-block keys (`key`, `forward`, `bypass`, `limiter`, `stale`) with no block or empty braces are an error; repeated keys stay an error and 08 §2 now says so; the `weir <matcher>` form is supported and documented; runtime placeholders (`{env.X}`, `{host}`) are not expanded, only parse-time `{$VAR}`, documented in 08 §2; the `name` error points at the `name` line. Tests added for repeated sub-block keys, negative durations, directive arguments, bare and empty sub-blocks, the matcher form. Kept: duplicate `name` handling in P2-02, the non-`card/*` branch, `RegisterDirectiveOrder` (TestDirectiveOrder fails loudly on a Caddy bump).

## Notes for the next session

- P2-03b: `nextOrigin` maps a 4xx from `next` to a response and other errors to a fixed-text 502. 5xx statuses chosen by `next` (a dial error from `reverse_proxy`) are still 502 with no body; check with caddytest that `handle_errors` output is right.
- The route warnings (`X-Forwarded-For`, per-client placeholders) run on the first request via `ctx.App("http")`; verify them against a real config in caddytest. Unit tests cover only the pure scan.
- The multi-host warning remembers one host (port stripped). Host case and port variants are treated as one site.
- Follow-up for 06 T-45/R-6: add the per-client placeholder case at its next revision.
- P2-01c: root `=.` replace stays until the next root release tag.
