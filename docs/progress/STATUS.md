# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-05-query-cookies-encoding
PR: pending
Next card: M1-06

## Blockers

none

## Waiting on Ashwin

- Confirm the coalesce-default clamp: a zero `LeaderMaxAge`/`FollowerMaxWait` now defaults to min(10s, `Timeouts.Origin`) instead of failing validation when the origin timeout is under 10s (01 §6 and 04 §1.1 updated).
- Approve the storetest API: `Run(t, newStore, opts ...Option)` with `WithoutEpochs()` and `Synctest()` (05 §8). `Synctest()` replaces the card's `func(d time.Duration)` advance hook, which cannot work: `synctest.Test` forbids `t.Run` inside a bubble and stores read `time.Now` (D9).
- Pragma with Cache-Control: `ParseRequest` sets `NoCache` from `Pragma: no-cache` even when the request also has `Cache-Control` (docs say "plus Pragma: no-cache" unconditionally). RFC 7234 §5.4 ignored Pragma when Cache-Control was present. Only matters with `Client.HonorRevalidation`. Keep as is, or ignore Pragma when Cache-Control is present (FR-SRV-8 wording change)?
- Resolve a conflict between CLAUDE.md hard rule 6 (no real-clock sleeps outside the `load` tag) and 05 §8 (remote stores run `ExpiredIsNotFound` on the real clock, now a 3 s sleep). Proposal: exempt remote-store conformance runs from rule 6, or run them only under an integration build tag.

- FR-VAL-1 and `#`: `Validate` accepts `#` (0x23) in `Path` and `RawQuery`, as FR-VAL-1 allows any byte in 0x21-0x7E. net/http passes a raw `#` through `RequestURI`, and `TransportOrigin` forwards it via `URL.Opaque`, so `GET /a#x?q` is keyed as path `/a#x`, query `q`, while nginx-style origins treat `#x?q` as a fragment and serve `/a`. Key and forward stay byte-equal, and browsers never send `#`, so this is cache fragmentation, not poisoning. Proposal: reject `#` in path and query, and `?` in path (RFC 9112 §3.2 origin-form), under the existing `path` and `query` reasons. Changes FR-VAL-1 wording, so it needs your approval.

- Keyed cookies and large Cookie headers: FR-VAL-3 applies `MaxKeyedHeaderBytes` (1 KiB) to all Cookie lines combined, so a 1.2 KiB analytics cookie makes a keyed `lang` absent, and the origin's default-language response is cached under the "no lang" key. Safe (no bypass) but wrong for common traffic. Options: measure only the keyed pairs' bytes, or give Cookie its own limit. Changes FR-VAL-3 wording.

## Notes for the next session

- M1-05 keys API: `rewriteQuery(raw, c) string`, `keyedCookies(lines, c) []Cookie` (present cookies in `Key.Cookies` order, a subsequence the key encoder can walk alongside the names), `cookieHeader([]Cookie) string` ("" means omit), `aeBucket(lines, c) string`. `keys.Config` now carries the query rules, `Cookies`, `AcceptEncoding` and `MaxKeyedHeaderBytes`.
- 04 §3.5 pseudocode shows `keyedCookies()` returning a string; update it when M1-07 wires forwarding.
- Query patterns are stored as strings and re-parsed per call. Nothing validates them yet: `"a*b"` is a literal, and `"*"` drops or keeps everything. `New` (config validation card) should validate them (04 §1.1 says `New` compiles query patterns).
- keys API (M1-04): `Validate(r *Request, c *Config) (host string, err error)` returns the normalized host; key and forwarded request must both use it (P2). Errors are `keys.ErrUpgrade` and `*keys.RequestError{Reason}`; M1-07 `Classify` (or the engine) maps them to `weir.ErrUpgradeNotSupported` and `*weir.RequestError`.
- httpcc API: `Lifetime`, `Jitter`, `StaleWindows`, `CorrectedInitialAge`, `CurrentAge`, `Evaluate`, `State` (zero `State` is invalid).
- `Evaluate` trusts `epOK`: the caller's `newestEpoch` applies FR-PRG-7 (04 §6.3). A zero or unknown `EpochMode` with `epOK` returns `Unusable`.
- FR-MODE-2 (`ModeStaleOnError`) must tell "stale forbidden" from "no SIE window" using `ep.Mode` and `e.Flags`; `sieOK` alone is false for both.
- Doc wording drift, not yet fixed: 01 FR-SRV-1 and FR-STL-3 say "unqualified `no-cache`", while the 01 RFC table and 04 `FlagNoCache` treat qualified and unqualified the same. The parser, `StaleWindows` and `Evaluate` follow the table.
- Memory store (M1-06 or wherever epochs land): keep `Epoch.At` as a `time.Now()` value with its monotonic reading. `Evaluate` computes `now.Sub(ep.At)`; a wall-only `At` lets a backward clock step shorten or cancel a soft purge.
- Codec (store/codec.go): `Evaluate` trusts `SWR`/`SIE` and does not re-check `FlagMustRevalidate`/`FlagProxyRevalidate`. The decoder should zero both windows when those flags are set, so corrupt bytes cannot enable stale serving (FR-STL-3).
- engine.go: `Close` does not wait for foreground `Serve` calls; decide before M1-12 closes an engine-owned store under them.
