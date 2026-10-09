# cache-tests expected failures

Updated: 2026-10-08

Tests from [http-tests/cache-tests](https://github.com/http-tests/cache-tests) that `examples/weirproxy` fails, from the run recorded in `testdata/cache-tests-baseline.json` (suite commit `d644cf4`, 365 tests, 264 pass, 101 fail). The nightly job (docs/07 §8) fails only when a test that passed in the baseline fails now, so this list is documentation, not configuration. Update both files together with `UPDATE=1 scripts/cache-tests.sh`.

The proxy runs with default Weir settings plus `-forward-allow Req-Num,Test-ID,Test-Name,Foo,Bar,Baz,Abc`. The first three are the suite's own bookkeeping headers; the others are request headers its Vary tests set. Without them strict forwarding (D4) hides the headers and the suite cannot run.

Passing everything means nothing (the suite's own words). Rows with a requirement ID but no D-number follow from the spec text, not from a separate decision; rows that cite RFC text or a card are decided in STATUS (2026-10-07, M10-04). The reasons were read off the requirements and the suite's request logs; they were not each traced through the code.

## By design

| Tests | Why Weir differs | Decision |
|---|---|---|
| `ccreq-ma0`, `ccreq-ma1`, `ccreq-magreaterage`, `ccreq-max-stale`, `ccreq-max-stale-age`, `ccreq-min-fresh`, `ccreq-min-fresh-age`, `ccreq-no-cache`, `ccreq-no-cache-etag`, `ccreq-no-cache-lm` | Client request directives that force revalidation are ignored unless `Client.HonorRevalidation` is set | D5, FR-SRV-8 |
| `ccreq-no-store` | Request `no-store` stops the response being stored; it does not bypass a stored entry | FR-SRV-7 |
| `stale-503`, `stale-close` | Stale is served only when the origin permits it (`stale-if-error`), and only for the error conditions in FR-STL-2 (a 503 or a closed connection with `max-age=2` alone is not one) | D6, FR-STL-2 |
| `stale-warning-become`, `stale-warning-stored` | The setup needs a stale reuse the origin did not permit; `Warning` is also obsolete in RFC 9111 | D6, D7 |
| `status-299-fresh`, `status-303-fresh`, `status-400-fresh`, `status-499-fresh`, `status-500-fresh`, `status-502-fresh`, `status-503-fresh`, `status-504-fresh`, `status-599-fresh`, `heuristic-599-cached` | Only the statuses in `Storable.Statuses` are stored | FR-STO-2, D39 |
| `method-POST`, `head-200-freshness-update`, `head-200-retain`, `head-200-update`, `head-410-update`, `head-writethrough` | Only a forwarded `GET` response is stored, so a `POST` or `HEAD` response never updates an entry | FR-STO-1 |
| `headers-store-Set-Cookie`, `other-set-cookie` | A response with `Set-Cookie` is not stored unless `Storable.StripSetCookie` is set | FR-STO-6, D31 |
| `conditional-etag-forward`, `conditional-etag-forward-unquoted`, `conditional-etag-vary-headers-mismatch` | Client `If-None-Match` is not forwarded on a cacheable request; only the keyed, allowed and protocol headers are | D4 |
| `304-etag-update-response-Content-Encoding`, `304-etag-update-response-Content-Type` | A 304 does not change `Content-Encoding` or `Content-Type`: they describe the stored body | FR-SRV-3 |
| `304-etag-update-response-Set-Cookie` | The setup response carries `Set-Cookie`, so it is never stored | FR-STO-6, D31 |
| `freshness-max-age-100a`, `freshness-max-age-a100`, `freshness-max-age-decimal-five`, `freshness-max-age-decimal-zero`, `freshness-max-age-two-fresh-stale-sameline`, `freshness-max-age-two-fresh-stale-sepline`, `freshness-max-age-two-stale-fresh-sameline`, `freshness-max-age-two-stale-fresh-sepline` | A non-integer `max-age`, or one repeated with different values, makes the lifetime zero | FR-FRS-2 |
| `freshness-max-age-date` | Age ignores `apparent_age`, so a skewed origin `Date` does not turn the cache off | FR-FRS-4, T-30 |
| `freshness-expires-wrong-case-tz` | HTTP dates are case-sensitive; `gMT` is an invalid `Expires`, a time in the past | FR-FRS-2 |
| `heuristic-delta-5`, `heuristic-delta-10`, `heuristic-delta-30` | The heuristic lifetime is 10% of `Date` minus `Last-Modified`, so 0.5 to 3 seconds here | FR-FRS-3 |
| `other-age-delay` | `Age` is added to responses served from a stored entry, not to the response that triggered the fetch | FR-FRS-7 |
| `conditional-etag-quoted-respond-unquoted`, `conditional-etag-unquoted-respond-quoted`, `conditional-etag-unquoted-respond-unquoted`, `conditional-etag-strong-generate-unquoted`, `conditional-etag-strong-respond-obs-text`, `conditional-etag-weak-respond-backslash`, `conditional-etag-weak-respond-lowercase`, `conditional-etag-weak-respond-omit-slash` | Entity tags are compared as RFC 9110 §8.8.3 defines them; unquoted, mis-cased or malformed tags never match | FR-SRV-2, FR-SRV-3 |
| `age-parse-numeric-parameter`, `age-parse-parameter` | An `Age` with parameters (`7200;foo=bar`) is not a delta-seconds value, so it is ignored (age 0) as RFC 9111 §5.1 says for an invalid `Age` | FR-FRS-4, RFC 9111 §5.1 |
| `headers-omit-headers-listed-in-Cache-Control-no-cache`, `headers-omit-headers-listed-in-Cache-Control-no-cache-single` | A qualified `no-cache="a"` is read as unqualified (validate before every reuse), the same strict reading as qualified `private` | FR-STO-4, FR-SRV-1 |
| `vary-normalise-space` | A keyed header value is compared exactly; `1,2` and ` 1, 2 ` are two variants. An "optimal" test; normalizing would be a key-boundary change | FR-KEY-9, P2 |
| `interim-102`, `interim-103`, `interim-no-header-reuse`, `interim-not-cached` | 1xx responses are not relayed to the client (the final response is cached correctly). Relaying is not a Phase 1 goal | none, noted in STATUS |
| `headers-store-Transfer-Encoding` | The suite sends an invalid `Transfer-Encoding` value and Go's HTTP client rejects the response, so Weir answers 502 for a malformed origin response | FR-FWD-7, `ErrOrigin` |
| `304-etag-update-response-ETag` | The suite reports a retry (`↻`), which its authors say needs manual interpretation; nothing is asserted | none |
| `conditional-lm-fresh-no-lm` | The suite sends `If-Modified-Since` 3000 s before the response's `Date` and the response has no `Last-Modified`. FR-SRV-2 compares the stored `Date`, which is 3000 s later than the client's `If-Modified-Since`, so the resource counts as modified and the answer is 200 (RFC 9110 §13.1.3). The test expects 304 | FR-SRV-2 |
| `pragma-response-no-cache-heuristic` | Go's `net/http` client rewrites a response `Pragma: no-cache` without `Cache-Control` into `Cache-Control: no-cache` before Weir sees it (`fixPragmaCacheControl`), so the engine reads a real `no-cache` and validates. The error is on the safe side, and telling the two apart would mean parsing responses outside the standard library | FR-FRS-3, RFC 9111 §5.4 |

## Not built yet

Each row turns into a pass when its card lands; rerun with `UPDATE=1` then.

| Tests | Missing piece | Decision |
|---|---|---|
| `cdn-expires-update-exceed`, `cdn-fresh-cc-nostore`, `cdn-max-age`, `cdn-max-age-0-expires`, `cdn-max-age-case-insensitive`, `cdn-max-age-cc-max-age-invalid-expires`, `cdn-max-age-expires`, `cdn-max-age-extension`, `cdn-max-age-long-cc-max-age`, `cdn-max-age-max`, `cdn-max-age-max-plus`, `cdn-max-age-short-cc-max-age`, `cdn-no-cache`, `cdn-no-store-cc-fresh`, `cdn-private`, `cdn-remove-age-exceed` | Targeted fields are Phase 1.x. Weir reads `Weir-Cache-Control` then `CDN-Cache-Control` (M12-02) | D12 |
| `partial-store-complete-reuse-partial`, `partial-store-complete-reuse-partial-no-last`, `partial-store-complete-reuse-partial-suffix` | Single-range 206 from a complete stored object (M11-01) | D11 |
| `partial-store-partial-complete`, `partial-store-partial-reuse-partial`, `partial-store-partial-reuse-partial-absent`, `partial-store-partial-reuse-partial-byterange`, `partial-store-partial-reuse-partial-suffix`, `partial-use-headers`, `partial-use-stored-headers` | Storing partial content is not planned; M11 serves ranges from complete objects only | D11, FR-STO-2 |
