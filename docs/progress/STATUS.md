# Status

Updated: 2026-09-27
Phase: 1
Current card: none
Card state: awaiting-merge
Branch: card/M1-06-key-encoding
PR: #12 https://github.com/AshwinSathian/weir/pull/12
Next card: M1-07

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

- M1-06 keys API: `PrimaryKey(*KeyInput) store.Key`; `KeyInput{Method, Scheme, Host, Path, Query, Headers []Header, CookieNames, Cookies}`. `Header{Name, Value, Present}` per `Key.Headers` name in config order; the Accept-Encoding bucket enters here as a normalized header value.
- `appendKey` pairs `Cookies` with `CookieNames` in one pass: pass `keyedCookies` output unchanged (config order). Reordering it drops cookie values from the key (pinned by `TestKeyedCookiesFeedEncoder`).
- Tags: `TagGlobal()`, `TagOrigin(o)`, `TagURI(o, p, q)`, `TagGroup(o, name)`; `o` is `scheme://host[:port]` after `normalizeHost`. No `Origin` builder yet; M1-07 builds `Classified.Origin`.
- `normalizePath(p)` is not wired yet. M1-07 must apply it (when `Key.NormalizePath`) to both the key path and the forwarded path (FR-FWD-5), and to the URI tag of ClassPass requests (04 §3.1). It returns paths with malformed escapes unchanged; Validate rejects those first.
- 04 §3.5 pseudocode shows `keyedCookies()` returning a string; update it when M1-07 wires forwarding.
- Query patterns are not validated yet: `New` (config validation card) should compile them (04 §1.1).
- `Evaluate` trusts `epOK`: the caller's `newestEpoch` applies FR-PRG-7 (04 §6.3). FR-MODE-2 must use `ep.Mode` and `e.Flags` to tell "stale forbidden" from "no SIE window".
- Memory store: keep `Epoch.At` with its monotonic reading. Codec: zero `SWR`/`SIE` when must-revalidate or proxy-revalidate flags are set (FR-STL-3).
- engine.go: `Close` does not wait for foreground `Serve` calls; decide before M1-12.
- Doc wording drift: 01 FR-SRV-1 and FR-STL-3 say "unqualified `no-cache`" while the 01 RFC table and 04 `FlagNoCache` treat both the same.
