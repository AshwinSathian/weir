# Weir technical specification

Status: v1.0, approved for Phase 0 and Phase 1 implementation
Date: 2026-10-01
Owner: Ashwin Sathian
Module: `github.com/AshwinSathian/weir`
Supersedes: the interface sketch in [00-design-doc.md §7.2](00-design-doc.md)

This document states what Weir must do. It is normative. [02-architecture.md](02-architecture.md) explains how the pieces fit, [03-hld.md](03-hld.md) walks the request flows, and [04-lld.md](04-lld.md) gives the exact types and algorithms. When this spec and a lower-level document disagree, this spec wins and the other document has a bug.

The key words MUST, MUST NOT, SHOULD, SHOULD NOT and MAY carry their RFC 2119 meaning. Every requirement has an ID (`FR-KEY-3`, `NFR-4`) so tests and commits can cite it.

## 1. Scope

Weir is a Go library that sits between an HTTP server and an origin and decides, per request, whether to answer from a shared cache, refresh in the background, coalesce with an in-flight fetch, fetch under a concurrency budget, serve stale, or shed. It implements the shared-cache half of RFC 9111 plus the resilience behaviors listed in the seed's failure-mode taxonomy (§6 of the seed, referred to below as T6.1 through T6.13).

Phase 1 delivers:

- the `weir` engine package (stdlib only),
- the `store` interface package and an in-process `store/memory` implementation,
- a `store/storetest` conformance suite,
- a `weirhttp` package that adapts the engine to `net/http` (middleware and a `RoundTripper`-backed origin), used as the reference adapter and as the integration-test harness.

Phase 1.x (milestones M11 to M15, §13) adds single-range responses, targeted cache-control fields, snapshots, per-host fairness and eager hard purge. Phase 2 adds the Caddy module in a separate Go module ([08-caddy-adapter-spec.md](08-caddy-adapter-spec.md)). Phase 2.5 adds a Valkey store in a separate Go module. Phase 3 adds experiment dimensions ([10-experiments-spec.md](10-experiments-spec.md)). Phases 2 and later are in scope here only through the extension points Phase 1 must leave open.

### 1.1 Goals

- G1. RFC 9111 shared-cache behavior for GET and HEAD, including validation (304), Vary, Age, and invalidation on unsafe methods.
- G2. No origin stampede from any of: a hot key expiring, many keys expiring together, a purge, a cold start, a storage outage, or an attacker generating guaranteed misses.
- G3. Origin outages degrade to stale content or fast errors, never to a queue of requests each paying the full origin timeout.
- G4. The cache key is a security boundary. No request input can influence the origin's response without also influencing the key.
- G5. Every decision is observable through one event stream and an RFC 9211 `Cache-Status` header.
- G6. The core module has zero third-party dependencies.

### 1.2 Non-goals

- Not a reverse proxy. Weir never opens sockets, terminates TLS, or speaks HTTP wire formats. Adapters do.
- Not a WAF, bot filter, or per-client rate limiter. Weir bounds origin load; it does not decide which clients deserve service. A flood of distinct paths can still consume the origin budget; see [06-threat-model.md](06-threat-model.md) T-12.
- Not a CDN. One process, one logical origin per `Engine`.
- Not a private (browser) cache. Weir is always a shared cache in the RFC 9111 sense.
- No partial-object storage. 206 responses from the origin are never stored. Weir answers single byte ranges from complete stored objects (M11, §13.1); multi-range requests get the full 200.
- No trailer storage. Trailers from the origin are discarded on stored responses (RFC 9111 §3.1 permits this).
- Targeted cache-control fields (RFC 9213) arrive in M12 (§13.2), not in M1 to M10.
- No persistence across crashes. Graceful shutdowns snapshot the memory store (M13, §13.3); `Warm` covers everything else.
- No multi-node cache coherence before Phase 2.5. Phase 2 deployments run one Caddy node (D17).

## 2. Decisions locked before this spec

These were settled with the project owner on 2026-09-27 and are not reopened by implementation work. Rationale and rejected alternatives live in [02-architecture.md §6](02-architecture.md).

| ID | Decision |
|---|---|
| D1 | The engine owns the origin fetch. Adapters pass an `Origin`; the engine runs coalescing, limiting, breaking and background refresh itself. The seed's `Decide`/`Complete` split is dropped. |
| D2 | The in-memory store is built in-repo: sharded, byte-weighted S3-FIFO. No cache library dependency. |
| D3 | The root module imports only the Go standard library. Metrics exporters, Valkey and Caddy live in separate modules. |
| D4 | Forwarding is strict by default: for cacheable requests the origin receives only headers that are keyed, explicitly allowed, or required by protocol. |
| D5 | Client request cache directives that force revalidation (`no-cache`, `max-age`, `min-fresh`, `max-stale`, `Pragma: no-cache`) are ignored by default. |
| D6 | Weir serves stale only when the origin permits it. Operator default stale windows exist but are zero by default. |
| D7 | The target spec is RFC 9111 (which obsoletes RFC 7234), with RFC 5861, RFC 9110, RFC 9211 and RFC 9875. |
| D8 | TTL jitter only ever shortens freshness. |
| D9 | Time-dependent tests use `testing/synctest`. There is no injected `Clock`. Randomness is injectable. |
| D10 | Minimum Go version is 1.27 (for `httptest.NewTestServer` and `synctest.Sleep`). |
| D11 | Single-range 206 from complete stored objects (M11). |
| D12 | Targeted fields `Weir-Cache-Control` then `CDN-Cache-Control` (M12), with `private`, `no-store` and `no-cache` in `Cache-Control` still binding (stricter than RFC 9213). |
| D13 | Static origin concurrency limit in Phase 1; opt-in adaptive limit considered after load-test data. |
| D14 | Phase order after Phase 1: Caddy adapter (2), then Valkey store (2.5). |
| D15 | Graceful-shutdown snapshot of the memory store, loaded as soft-stale (M13). |
| D16 | Per-host fairness: limiter per-host cap and memory-store per-owner byte quota (M14), off by default in the library, on in the Caddy adapter for multi-host sites. |
| D17 | Phase 2 runs a single Caddy node; multi-node waits for the Valkey store. |
| D18 | Hard purge is lazy; `Purge.Eager` additionally deletes matching records now (M15). |
| D19 | Remote-store epoch lookups fail open with an event (T-9). |
| D20 | `Cache-Status` on by default with minimal parameters. |
| D21 | `Key.AcceptEncoding` default stays `["gzip"]`; docs steer operators to list what their origin produces. |
| D22 | Negative caching on by default, 2 s. |
| D23 | Prometheus is the first exporter module. |
| D24 | Repository private on GitHub from Phase 0, public with `v0.x` tags from M1, `v1.0.0` at the end of Phase 1. |
| D25 | Requests that carry a body use a separate upload limiter pool; adapters must bound request bodies and read time (§14.1). |
| D26 | Origin timeout covers headers and buffered bodies; streamed bodies are bounded by an idle timeout, not a total (§14.2). |
| D27 | Upgrade and `CONNECT` requests are rejected by `Serve`; adapters route them around Weir (§14.3). |
| D28 | `text/event-stream` responses are never buffered, coalesced or stored (§14.4). |
| D29 | `traceparent`, `tracestate` and `X-Request-Id` are forwarded by default in addition to `Forward.Allow`; `Forward.NoTraceHeaders` turns them off; malformed `traceparent` is dropped (§14.5). |
| D30 | `Key.QueryDrop` stays empty by default; `weir.TrackingParams` preset provided. |
| D31 | No default cookie bypass; a sampled report of stripped cookie names helps operators configure `Bypass` (§14.6). |
| D32 | No prefix purge; sections are purged through `Cache-Groups`. |
| D33 | Runtime incident modes via `Engine.SetMode` with a mandatory expiry (§14.7). |
| D34 | Caddy adapter returns `caddyhttp.Error` so `handle_errors` applies. |
| D35 | Default memory-store size derives from `GOMEMLIMIT` when set (§14.8). |
| D36 | Store stays on the Go heap; GC cost measured in M10 before any layout change. |
| D37 | Vary overflow refuses new variants and reclaims slots of expired or evicted ones. |
| D38 | Hit-for-miss TTL stays 30 s. |
| D39 | 302 and 307 are storable by default, only with explicit freshness. |
| D40 | Vulnerabilities reported through GitHub private vulnerability reporting; `SECURITY.md` at M1. |
| D41 | License Apache-2.0. |
| D42 | Support the two latest Go releases; the minimum rises only when it leaves that window. |

## 3. Terminology

Terms from the seed glossary ([00-design-doc.md §12](00-design-doc.md)) apply. Additional terms:

- Primary key: the SHA-256 digest of the canonical encoding of the request's keyed inputs (method class, scheme, host, path, rewritten query, configured key headers and cookies).
- Vary spec: a small stored record, keyed by the primary key, listing the header names the origin's latest response named in `Vary`.
- Variant key: the digest of the primary key plus the normalized values of the vary-spec headers taken from the forwarded request.
- Forwarded request: the request Weir hands to the `Origin`. It is derived from the client request by the rewrite rules in §5.3 and is the only input the key is computed from.
- Flight: one in-progress origin fetch shared by a leader and zero or more followers.
- Partition: the scheme, host and path of a request, without the query. Used for fairness and miss-rate tracking.
- Epoch: a timestamp and mode (soft, invalid, hard) attached to a tag. An entry stored before its tag's epoch is treated as soft-stale, invalid, or purged. Every entry has three implicit tags (global, origin, URI) plus its `Cache-Groups`.
- Origin-health failure: a transport error, an origin timeout, or a response with status 502, 503 or 504. These are the only failures that move the circuit breaker or create negative entries.

## 4. Public API

Exact signatures. Unexported details are in [04-lld.md](04-lld.md).

```go
package weir

// New validates cfg, applies defaults and returns a ready engine.
// It returns an error wrapping ErrInvalidConfig for any invalid field.
func New(cfg Config) (*Engine, error)

// Serve answers one request. It never returns (nil, nil). On a nil error the
// caller owns resp.Body and MUST close it. A non-nil error means no response
// could be produced; StatusCode(err) gives the status an adapter should send.
func (e *Engine) Serve(ctx context.Context, req *Request, origin Origin) (*Response, error)

// Purge invalidates by tag, by URL, or everything. Soft purge is the default.
func (e *Engine) Purge(ctx context.Context, p Purge) error

// Warm fetches each request through the normal path at background priority
// and stores what is storable. It stops at the first ctx cancellation.
func (e *Engine) Warm(ctx context.Context, reqs iter.Seq[*Request], origin Origin) (WarmStats, error)

// Close rejects new Serve/Warm calls with ErrClosed, waits for background
// fetches until ctx is done, then cancels the rest. It closes the store only
// if the engine created it.
func (e *Engine) Close(ctx context.Context) error

// SetMode switches incident modes for ttl (required, at most 24 h); see §14.7.
func (e *Engine) SetMode(m Mode, ttl time.Duration) error

// Stats returns point-in-time gauges for exporters: in-flight origin fetches,
// queued fetches, breaker state, and store bytes when the store reports them.
func (e *Engine) Stats() EngineStats

// Origin produces responses for forwarded requests. Fetch MUST honor ctx:
// return promptly once ctx is done, and make body reads fail after that.
// Close depends on it (FR-LCY-2). Fetch may be called after the request that
// triggered it has finished (background refresh), and concurrently.
type Origin interface {
	Fetch(ctx context.Context, req *Request) (*Response, error)
}

type OriginFunc func(ctx context.Context, req *Request) (*Response, error)

func (f OriginFunc) Fetch(ctx context.Context, req *Request) (*Response, error)

type Request struct {
	Method   string      // exactly as received; methods are case-sensitive
	Scheme   string      // "http" or "https"
	Host     string      // authority (host[:port]) the client addressed
	Path     string      // origin-form path, still percent-encoded, as received
	RawQuery string      // query without the leading '?', as received
	Header   http.Header // never mutated by Weir
	Body     io.ReadCloser // nil for GET and HEAD; passed through for other methods
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser // never nil on responses returned by Serve
	Cache      CacheInfo     // set by Serve; ignored on responses returned by an Origin
}

type CacheInfo struct {
	Hit       bool          // answered without contacting the origin for this request
	Fwd       FwdReason     // why the request went forward; FwdNone when Hit
	Stale     StaleReason   // non-zero when a stale response was served
	FwdStatus int           // origin status when Fwd != FwdNone (304 on revalidation)
	Stored    bool          // the forwarded response was stored
	Collapsed bool          // this request waited on another request's flight
	TTL       time.Duration // remaining freshness at send time; negative when stale
	Detail    string        // implementation detail for Cache-Status, e.g. "negative"
}
```

`Purge` (fields `Mode`, `All`, `URLs`, `Origin`, `Groups`, and `Eager` from M15), `WarmStats` and `EngineStats` are defined in [04-lld.md §1.2](04-lld.md).

`FwdReason` values mirror RFC 9211 §2.2: `FwdNone`, `FwdBypass`, `FwdMethod`, `FwdURIMiss`, `FwdVaryMiss`, `FwdRequest`, `FwdStale`. `StaleReason` values: `StaleNone`, `StaleWhileRevalidate`, `StaleIfError`, `StaleShed`, `StaleCircuitOpen`, `StaleCoalesceTimeout`.

Errors (all comparable with `errors.Is`):

| Error | Meaning | `StatusCode(err)` |
|---|---|---|
| `ErrInvalidRequest` (wrapped in `*RequestError` carrying a reason) | the request failed input validation (§5.1) | 400 |
| `ErrShed` | no origin slot within the queue budget and nothing stale to serve | 503 |
| `ErrCircuitOpen` | breaker open and nothing stale to serve | 503 |
| `ErrOriginTimeout` | the origin did not answer within `Timeouts.Origin` | 504 |
| `ErrMustRevalidate` | a `must-revalidate` entry could not be validated | 504 |
| `ErrOnlyIfCached` | `only-if-cached` request with no usable stored response | 504 |
| `ErrOrigin` (wrapped in `*OriginError`) | the origin returned a transport error | 502 |
| `ErrClosed` | the engine is closed | 503 |
| `ErrUpgradeNotSupported` | `CONNECT` or protocol upgrade reached `Serve` (FR-UPG-1) | 501 |
| `context.DeadlineExceeded` | the caller's context expired | 504 |
| `context.Canceled` | the caller went away | 499 (log hint only; adapters should not write) |
| `ErrInvalidConfig` | returned by `New` only | n/a |

`func StatusCode(err error) int` maps any error to the status above, and to 502 for unknown errors. Shed and circuit-open errors are returned wrapped in `*RetryError{Err, After}`; `func RetryAfter(err error) (time.Duration, bool)` extracts `After` for the adapter's `Retry-After` header.

Package `store` (import path `github.com/AshwinSathian/weir/store`) defines `Store`, `Entry`, `Key`, `Epoch` and the sentinel errors `ErrNotFound` and `ErrUnavailable`. Its full contract is [05-storage-interface-spec.md](05-storage-interface-spec.md).

## 5. Functional requirements

### 5.1 Request validation (T6.9)

- FR-VAL-1. `Serve` MUST reject, with `ErrInvalidRequest`, a request whose `Scheme` is not `http` or `https`; whose `Host` is empty, longer than 255 bytes, or not a syntactically valid `host[:port]` (RFC 3986 reg-name, IPv4, or bracketed IPv6, port 1–65535); whose `Path` does not start with `/` (the only exception is `*` with method `OPTIONS`), exceeds `Limits.MaxPathBytes` (default 8 KiB), contains any byte outside 0x21–0x7E, contains a raw `#` or `?`, or has a malformed percent escape; whose `RawQuery` exceeds `Limits.MaxQueryBytes` (default 8 KiB), has more than `Limits.MaxQueryParams` (default 256) `&`-separated segments, contains any byte outside 0x21–0x7E, or contains a raw `#` (RFC 9112 §3.2: origin-form has no fragment, and an origin would read one, so `/a#x` would split the cache for `/a`; the escaped `%23` and `%3F` stay valid); or whose `Method` is not an RFC 9110 token.
- FR-VAL-2. Validation MUST run before any store access, flight lookup or origin contact. A rejected request costs no origin capacity.
- FR-VAL-3. For every header that participates in the key or is rewritten for forwarding, Weir MUST apply that header's normalizer (§5.2.3). A normalizer MUST map every malformed, oversized (over `Limits.MaxKeyedHeaderBytes`, default 1 KiB after combining lines), or non-ASCII value to one fixed fallback: "absent" for generic headers (the forwarded request then omits the header) and `identity` for `Accept-Encoding`. For `Cookie` the limit applies to the keyed pairs as forwarded (FR-KEY-6), not to the received lines: unkeyed cookies do not count, and keyed pairs over the limit make every keyed cookie absent. Key and forwarded request always carry the same fallback. Malformed keyed headers never cause a bypass, a rejection, or a distinct cache key per malformed value.
- FR-VAL-4. No input, however malformed, may cause a panic. Every parser in the request path has a fuzz target ([07-testing-strategy.md §5](07-testing-strategy.md)).

### 5.2 Cache key (T6.7, T6.9)

- FR-KEY-1. The primary key MUST be SHA-256 over a canonical encoding in which every field is tagged and length-prefixed. No two distinct tuples of keyed inputs may encode to the same bytes. String concatenation with delimiters is forbidden.
- FR-KEY-2. The primary key inputs are exactly: method class (`GET` and `HEAD` share class `GET`), lowercase scheme, normalized host, path, rewritten query, and the configured `Key.Headers` and `Key.Cookies` values after normalization. Nothing else enters the primary key.
- FR-KEY-3. Host normalization: ASCII lowercase, default port removed (`:80` for http, `:443` for https), trailing dot removed. No IDNA processing.
- FR-KEY-4. The path is used byte-for-byte as received unless `Key.NormalizePath` is true, in which case percent-encoded unreserved characters are decoded and remaining percent escapes are uppercased (RFC 3986 §6.2.2.1–6.2.2.2). Dot-segments are never resolved. Whatever form the key uses, the forwarded request MUST carry the identical path bytes.
- FR-KEY-5. Query handling operates on raw `&`-separated segments without decoding. A segment's name is the bytes before the first `=`. `Key.QueryDrop` removes matching segments; `Key.QueryKeep`, when non-empty, removes all non-matching segments; `Key.QuerySort` sorts remaining segments by raw bytes (stable). Name patterns match exactly or by trailing `*` prefix (`utm_*`). The resulting query is used in the key and MUST be the query forwarded to the origin. Empty segments are dropped. A query that becomes empty is forwarded without `?`.
- FR-KEY-6. Cookies listed in `Key.Cookies` are extracted from all `Cookie` header lines (RFC 6265 `name=value` pairs separated by `;`). If a named cookie appears more than once with different values, it is treated as absent. The forwarded `Cookie` header MUST contain only the keyed cookies, in `Key.Cookies` order.
- FR-KEY-7. Vary handling uses a two-level lookup: a vary spec stored under the primary key and variants stored under variant keys. Secondary values are read from the forwarded request (after rewriting), never from the raw client request.
- FR-KEY-8. `Vary: *` MUST prevent storage (RFC 9111 §4.1).
- FR-KEY-9. In `VaryAuto` mode (default), any header named in `Vary` is folded into the variant key, except the sensitive set `Cookie`, `Authorization` and `Proxy-Authorization`, which prevent storage unless listed in `Key.VaryAllow`. In `VaryStrict` mode, a response whose `Vary` names any header not in `Key.VaryAllow` MUST NOT be stored. No configuration can make Weir store a response under a key that omits a header its `Vary` names.
- FR-KEY-10. A response whose `Vary` lists more than `Key.MaxVaryHeaders` (default 8) names MUST NOT be stored. When a primary key already has `Key.MaxVariants` (default 8) live variants, a response for a new variant MUST NOT be stored; the request is answered and counted as `vary-overflow`. A variant is live while its ref has not passed `Expires` and its record still exists in the store; dead refs are dropped at the next spec update, freeing their slots (D37).
- FR-KEY-11. Secondary header normalization for Vary follows RFC 9111 §4.1: lines combined with `, `, optional whitespace around commas removed, leading and trailing whitespace removed. Headers with a registered normalizer (§5.2.3) use it. An absent header only matches absent.
- FR-KEY-12. The key builder MUST be deterministic across processes (no per-process seed in key bytes) so a shared store in Phase 2.5 sees the same keys from every node.

#### 5.2.3 Header normalizers

- `Accept-Encoding`: parsed per RFC 9110 §12.5.3. The value becomes the single most preferred coding from `Key.AcceptEncoding` (default `["gzip"]`) that has a non-zero qvalue, explicitly or via `*`; ties break by list order. If none qualifies the value is `identity`. A request without `Accept-Encoding` also gets `identity`: RFC 9110 permits any coding in that case, but clients that omit the header often cannot decode one. Malformed list members are skipped; a wholly malformed value becomes `identity`. The forwarded request carries `Accept-Encoding: <chosen>`. `Accept-Encoding` is always processed this way, keyed or not, because it is the CVE-2024-35296 input.
- Other keyed headers: trimmed, list lines combined with `, `, internal runs of optional whitespace around commas collapsed. Case preserved (header values are case-sensitive unless their spec says otherwise).
- Operators cannot register custom normalizers in Phase 1. (Phase 3 key dimensions will use the same hook.)

### 5.3 Forwarded request (T6.7)

- FR-FWD-1. For `GET` and `HEAD` requests that are not bypassed (§5.9), in `ForwardStrict` mode (default), the forwarded request MUST contain only: headers in `Key.Headers`; `Cookie` rewritten per FR-KEY-6 (omitted if no keyed cookies); `Accept-Encoding` per §5.2.3; `Authorization`; `Cache-Control` and `Pragma` (RFC 9111 §5.2 requires proxies to pass cache directives through); headers in `Forward.Allow`; and conditional headers Weir itself adds for validation. Client `If-None-Match`, `If-Modified-Since`, `If-Match`, `If-Unmodified-Since`, `If-Range` and `Range` are removed. The forwarded request has no body, whatever the client sent (T-5, fat GET).
- FR-FWD-2. In `ForwardAll` mode, all client headers are forwarded except hop-by-hop fields (RFC 9110 §7.6.1: `Connection`, fields named in `Connection`, `Keep-Alive`, `Proxy-Connection`, `TE`, `Transfer-Encoding`, `Upgrade`, plus `HTTP2-Settings`, which RFC 9113 §3.1 ties to an `h2c` upgrade) and the conditional and range headers listed above. `ForwardAll` weakens G4; `New` MUST log a warning when it is set.
- FR-FWD-3. Unsafe and unknown methods, bypassed requests, and range requests on a miss are forwarded with all client headers except hop-by-hop fields, and with the body.
- FR-FWD-4. `HEAD` requests on a miss are forwarded as `GET` so the response can be stored; the client receives headers only.
- FR-FWD-5. The forwarded request's `Path` and `RawQuery` are exactly the key inputs from FR-KEY-4 and FR-KEY-5.
- FR-FWD-7. Every origin response loses its hop-by-hop fields (the FR-FWD-2 list) and the fields its `Connection` names before `Serve` returns it or stores it, on every path: miss, validation, pass-through and event stream. The strip happens once, in the engine's single origin call (P3), so each adapter gets it without repeating it; adapters may strip again because they own the connection. Every engine decision still reads the response as received: storage (§5.4), freshness and `Age`, event-stream detection (FR-STR-1) and invalidation (FR-INV-1). A field that `Connection` names (for example `Cache-Control: private`, `Vary`, `Set-Cookie`, `Age` or `Location`) has the effect it would have unnamed (T-8), and is then absent from the served and stored response.

### 5.4 Storability (RFC 9111 §3, §3.5)

A response is stored only if all of the following hold. Each failed check increments the `not_stored` event with a reason.

- FR-STO-1. The forwarded method was `GET`.
- FR-STO-2. The status is in `Storable.Statuses`. Default: the RFC 9110 heuristically cacheable set `200, 203, 204, 300, 301, 308, 404, 405, 410, 414, 501` plus `302` and `307` (D39), which are stored only with explicit freshness and never heuristically. 206 and 304 are never stored as new entries.
- FR-STO-3. Neither the request nor the response carries `no-store`, except that a response with `must-understand` and a status in `Storable.Statuses` ignores `no-store` (RFC 9111 §5.2.2.3).
- FR-STO-4. The response has no `private` directive. The qualified form `private="field"` is treated as unqualified.
- FR-STO-5. If the forwarded request carried `Authorization`, the response has `public`, a valid `s-maxage` or `must-revalidate`. An `s-maxage` is valid when its argument is delta-seconds and no other rule of FR-FRS-2 makes the lifetime zero (no invalid or conflicting delta-seconds directive in the field). RFC 9111 §4.2.1 makes any other form unusable, so it grants no §3.5 permission: a zero-lifetime entry stored under it could be served stale to another user (T-8).
- FR-STO-6. The response has no `Set-Cookie` field, unless `Storable.StripSetCookie` is true, in which case `Set-Cookie` is removed before storing and still passed to the client that triggered the fetch.
- FR-STO-7. Vary checks FR-KEY-8 to FR-KEY-10 pass.
- FR-STO-8. The response has explicit freshness (`s-maxage`, `max-age`, or `Expires`), or a heuristic lifetime from §5.5, or `public`, or a validator (`ETag` or `Last-Modified`) together with `no-cache` or a zero lifetime. A response with none of these is not stored.
- FR-STO-9. The complete body was received and its size plus header size is at most `Storable.MaxObjectBytes` (default 1 MiB). Oversized bodies are streamed to the requesting client and not stored.
- FR-STO-10. The `Cache-Groups` field, if present, parses as an RFC 9651 List of Strings with at most `Limits.MaxGroups` (32) members of at most `Limits.MaxGroupBytes` (128) bytes each. A response whose groups exceed these limits is not stored, because a purge could then miss it.
- FR-STO-11. Stored headers exclude hop-by-hop fields, fields named in `Connection`, `Proxy-Authenticate`, `Proxy-Authentication-Info`, `Age` (recomputed on serve), and `Set-Cookie` (when stripped).
- FR-STO-12. When a response is not storable and no stored response exists at the coalescing key (a marker must not displace a response that could still be revalidated or served under stale-if-error), Weir stores a hit-for-miss marker under the coalescing key (FR-COA-1) for `Coalesce.HitForMissTTL` (default 30 s). Requests that find a marker skip coalescing and go straight to the origin (still through the limiter). A later storable response replaces the marker. Markers are written only when the non-storability comes from the response under keyed inputs: never for a request that carried `Authorization` or a `no-store` request directive, never for a request that forwarded a `Forward.Allow` field, and never under `ForwardAll`, because all of these reach the origin unkeyed and would let one client disable coalescing of a URL for everyone, renewably (T-31). Trace headers alone do not suppress markers. The cost falls on operators who opt into unkeyed forwarding: on those requests an uncacheable URL gets no marker, so it coalesces and followers re-enter (FR-COA-5).
- FR-STO-13. A response without a valid `Date` gets `Date` set to the time it was received before it is stored (RFC 9110 §6.6.1).

### 5.5 Freshness and age (RFC 9111 §4.2)

- FR-FRS-1. Freshness lifetime, first match wins: `s-maxage`; `max-age`; `Expires` minus `Date` (response time if `Date` is absent or invalid); heuristic.
- FR-FRS-2. A directive that appears more than once with different values, or has a non-integer or negative argument, makes the lifetime zero. `delta-seconds` values above 2147483648 are clamped to 2147483648 (RFC 9111 §1.2.2). An invalid `Expires` (including `0`) is a time in the past.
- FR-FRS-3. Heuristic lifetime applies only to statuses in the heuristically cacheable set without explicit freshness, and is `Freshness.HeuristicFraction` (default 0.1) of `Date` minus `Last-Modified`, capped at `Freshness.HeuristicMax` (default 1 h). Without `Last-Modified` the heuristic lifetime is `Freshness.DefaultTTL` (default 0, meaning not stored unless FR-STO-8 allows it via a validator).
- FR-FRS-4. Age follows RFC 9111 §4.2.3 using the `corrected_age_value` form the RFC permits: `corrected_initial_age = age_value + response_delay`, `current_age = corrected_initial_age + (now - response_time)`. The conservative `max(apparent_age, ...)` form is not used, because `apparent_age` compares the origin's `Date` with the local clock: an origin whose clock runs 10 minutes slow would otherwise make every `max-age=300` response stale on arrival and turn the cache off (T-30).
- FR-FRS-5. Jitter (T6.1): when a response is stored or freshened, Weir computes `lifetime_eff = lifetime × (1 − Freshness.Jitter × U)` with `U` uniform in [0, 1) and `Freshness.Jitter` defaulting to 0.10. Jitter applies only when `lifetime ≥ Freshness.JitterMinLifetime` (default 10 s) and is disabled by `Freshness.NoJitter`. It MUST NOT lengthen any lifetime. Stale windows are measured from the jittered expiry.
- FR-FRS-6. Early refresh (XFetch): on a fresh hit whose remaining lifetime is `r`, Weir starts a background refresh when `−Δ × Freshness.EarlyRefreshBeta × ln(U) ≥ r`, where `Δ` is the entry's last fetch duration clamped to [1 ms, 10 s] and `U` is uniform in (0, 1]. Default beta is 1.0. Disabled by `Freshness.NoEarlyRefresh`, and never triggered for lifetimes below `Freshness.JitterMinLifetime`.
- FR-FRS-7. Every response served from a stored entry MUST carry an `Age` header equal to its current age in whole seconds (RFC 9111 §4).
- FR-FRS-8. In-process time arithmetic (age, staleness, epoch comparison in the memory store) uses Go's monotonic clock readings, so a wall-clock step does not extend freshness or make a purge miss entries. Negative intermediate ages clamp to zero. Wall-clock values are used only where they cross a process boundary (codec, remote stores) or come from the origin (`Date`, `Expires`, `Last-Modified`).

### 5.6 Serving from cache and validation

- FR-SRV-1. A fresh entry is served without contacting the origin unless it carries unqualified `no-cache`, in which case it is validated first.
- FR-SRV-2. For client conditional requests on a hit, Weir evaluates `If-None-Match` (weak comparison) and, if absent, `If-Modified-Since` against the stored response and answers 304 when the precondition fails (RFC 9111 §4.3.2, RFC 9110 §13.2.2). This applies only to stored 200 responses. `If-Modified-Since` without a stored `Last-Modified` uses the stored `Date`. The 304 carries the stored `Cache-Control`, `Content-Location`, `Date`, `ETag`, `Expires` and `Vary` fields (RFC 9110 §15.4.5), plus `Age` and `Cache-Status`.
- FR-SRV-3. To validate a stored entry Weir sends `If-None-Match` with the stored `ETag` and `If-Modified-Since` with the stored `Last-Modified` when present. On 304, it freshens the entry per RFC 9111 §4.3.4 (stored headers updated from the 304 except `Content-Length`, `Content-Encoding` and `Content-Type`, which describe the stored body; RFC 9110 §15.4.5 says a 304 should not carry other representation metadata, and keeping the stored values keeps body and labels consistent even under a weak `ETag`), recomputes freshness with new jitter, and serves it. If the 304 carries a strong `ETag` that differs from the stored one, the stored entry is not updated (§4.3.4) and Weir repeats the request unconditionally under the same limiter slot. On a full response it applies §5.4. On an origin-health failure it applies §5.8.
- FR-SRV-4. `HEAD` requests are answered from `GET` entries without a body.
- FR-SRV-5. A request with `Range` is answered from a stored entry with the full 200 response when the entry is fresh or servable under SWR (RFC 9110 lets a server ignore `Range`). Otherwise it is forwarded with its `Range` and `If-Range` fields (so the origin applies `If-Range`, RFC 9110 §13.1.5), is not coalesced, is not stored, and does not create hit-for-miss markers.
- FR-SRV-6. `only-if-cached` in the request is always honored: a usable stored response or `ErrOnlyIfCached`.
- FR-SRV-7. Request `no-store` is always honored: the response is not stored.
- FR-SRV-8. With `Client.HonorRevalidation` false (default), request `no-cache`, `max-age`, `min-fresh`, `max-stale` and `Pragma: no-cache` do not change lookup behavior. With it true, `no-cache`/`max-age=0` force validation of a stored entry, and so does `Pragma: no-cache` when the request has no `Cache-Control` field (RFC 9111 §5.4); the validation still goes through coalescing and the limiter.
- FR-SRV-9. Unless `NoCacheStatus` is set, every response produced from cache or after forwarding carries a `Cache-Status` member (RFC 9211) named `CacheStatus` (default `"Weir"`) appended to any existing field value, with `hit` or `fwd`, `fwd-status`, `stored`, `collapsed`, `ttl`, and `detail` where applicable. The `key` parameter is never emitted. Negative responses (§5.14) carry `hit; detail=negative` even though RFC 9211 §2 advises against annotating locally generated responses; they are derived from a stored record, and operators need to tell them apart from origin errors.

### 5.7 Coalescing (T6.2, T6.2a)

- FR-COA-1. Concurrent requests that resolve to the same coalescing key share one flight. The coalescing key is the variant key when a vary spec is already stored, otherwise the primary key.
- FR-COA-2. The origin fetch of a flight runs on its own goroutine with a context detached from every requester (`context.WithoutCancel` plus `Timeouts.Origin`). No requester's cancellation cancels the shared fetch.
- FR-COA-3. A flight older than `Coalesce.LeaderMaxAge` (default 10 s) is aged. New requests for that key do not join an aged flight; the first one starts a new flight. At most one new flight per key per `LeaderMaxAge` results.
- FR-COA-4. A follower waits at most `Coalesce.FollowerMaxWait` (default 10 s), bounded by its own context. On timeout it serves a stale entry if §5.8 permits (reason `coalesce-timeout`), otherwise it performs its own fetch through the limiter, and that fetch's storable result is stored.
- FR-COA-5. When a flight's response is not reusable for a follower (not storable, oversized, or its Vary does not match the follower), the follower re-enters lookup once. On the second pass it may coalesce again only if its coalescing key changed (typically because the first flight stored a vary spec and the follower now resolves to a different variant key), otherwise it fetches independently. Followers never wait on each other serially. A storable flight response is shared even when it needs validation before its next reuse (`no-cache`, `max-age=0`), as Varnish and nginx do: a strict reading of RFC 9111 §4 would have each follower refetch, which ends coalescing for typical `no-cache` HTML (decided 2026-10-01).
- FR-COA-6. A panic in `Origin.Fetch` is recovered, converted to `*OriginError`, and delivered to every waiter. The limiter slot and flight entry are always released.
- FR-COA-7. The flight table is independent of the store. Coalescing keeps working while the store is unavailable.
- FR-COA-8. A request carrying `Authorization` that finds no usable stored entry is not coalesced; it fetches directly. A response fetched with one client's credentials is shared with another request only through the store, and only when FR-STO-5 allowed storing it.
- FR-COA-9. The flight's fetch context carries the values of the creating request's context (adapters may need them, for example Caddy's replacer) but none of its cancellation. Only `Close` cancels it.

### 5.8 Stale serving and errors (T6.6)

- FR-STL-1. Stale-while-revalidate: a stale entry whose staleness is within its SWR window is served immediately and a background refresh is started (if none is in flight for the key). SWR is permitted by the response's `stale-while-revalidate`, or by `Freshness.DefaultStaleWhileRevalidate` when the response has none of `stale-while-revalidate`, `must-revalidate`, `proxy-revalidate`, `no-cache`, `s-maxage`.
- FR-STL-2. Stale-if-error: on an error condition, a stale entry whose staleness is within its SIE window is served. Error conditions are: origin-health failure, status 500, breaker open, shed, and follower coalesce timeout. SIE is permitted by the response's `stale-if-error`, or by `Freshness.DefaultStaleIfError` when the response has none of `stale-if-error`, `must-revalidate`, `proxy-revalidate`, `no-cache`, `s-maxage`. Each default is decided on its own: an origin `stale-while-revalidate` does not suppress `DefaultStaleIfError`, and the reverse.
- FR-STL-3. Stale serving is forbidden for entries with `must-revalidate`, `proxy-revalidate`, or unqualified `no-cache`. `s-maxage` without an explicit stale directive also forbids it. An explicit origin `stale-while-revalidate` or `stale-if-error` is honored alongside `s-maxage`.
- FR-STL-4. A `must-revalidate` or `proxy-revalidate` entry whose validation cannot complete (origin-health failure, shed, or breaker open) yields `ErrMustRevalidate` (504), per RFC 9111 §5.2.2.2. When the origin itself answered with a 5xx response, that response is passed through instead.
- FR-STL-5. Entries invalidated by an unsafe method (§5.12) are never served stale.
- FR-STL-6. Background refreshes are only ever triggered by an incoming request or by `Warm` (RFC 5861 §5).

### 5.9 Bypass

- FR-BYP-1. `Bypass` rules (`Bypass.Cookies []string`: presence of any named cookie; `Bypass.Headers []string`: presence of any named header) mark a `GET`/`HEAD` request as bypassed. Bypassed requests are forwarded per FR-FWD-3, never stored, never coalesced, never served from cache, and still go through the limiter and breaker. `Cache-Status` reports `fwd=bypass`.

### 5.10 Origin concurrency limiter (T6.3, T6.4, T6.5, T6.8)

- FR-LIM-1. Every origin fetch, including unsafe methods, bypass, background refresh and `Warm`, holds a limiter slot from before the request is sent until the response body is fully buffered. Streamed responses (pass-through and bodies over `MaxObjectBytes`) release the slot when response headers arrive, so a slow-reading client cannot pin origin slots; the stream itself stays bounded by `Timeouts.Origin`. At most `Limiter.MaxConcurrent` (default 64) fetches hold slots at once.
- FR-LIM-2. Foreground fetches that cannot start immediately queue FIFO. The queue holds at most `Limiter.MaxQueue` (default 1024) waiters; each waits at most `Limiter.MaxQueueWait` (default 2 s) or its context. A full queue or an expired wait is a shed.
- FR-LIM-3. At most `Limiter.MaxPerPartition` (default 16) fetches for one partition are in flight at once. A request over its partition cap queues like any other, and is skipped (not reordered past) until its partition has room. At most max(`MaxPerPartition`, `MaxQueue`/4) waiters of one partition (256 by default) are queued at once; a request past that is shed as a full queue, so a flood on one path cannot take more than a quarter of the queue places every other path shares (T-11). The quarter, not `MaxPerPartition`, keeps a legitimate cold start on one path with many query strings queueing: with 16 it served 32 of 2 000 such requests in the 2 s queue wait, against 656 with no cap.
- FR-LIM-4. Background fetches never queue. They start only if in-flight fetches are below `MaxConcurrent − ReserveForeground`, where `ReserveForeground` defaults to 25% of `MaxConcurrent`; otherwise the refresh is dropped and counted.
- FR-LIM-5. On shed, Weir serves stale per FR-STL-2 (reason `shed`) or returns `ErrShed` with a `RetryAfter` hint of `MaxQueueWait`.
- FR-LIM-6. Limiter state is bounded: memory is O(`MaxConcurrent` + `MaxQueue`) regardless of the number of distinct partitions seen.

### 5.11 Circuit breaker (T6.6)

- FR-CB-1. One breaker per `Engine`. It counts outcomes of all origin fetches over a rolling window of `Breaker.Window` (default 10 s, 10 buckets).
- FR-CB-2. The breaker opens when the window holds at least `Breaker.MinRequests` (default 20) outcomes and the fraction of origin-health failures is at least `Breaker.FailureRatio` (default 0.5). Status 500 and 4xx never count as failures. `Breaker.CountStatus500` makes 500 count.
- FR-CB-3. Open lasts `Breaker.OpenFor` (default 5 s) with ±20% jitter. Each consecutive reopen doubles the duration up to `Breaker.MaxOpenFor` (default 60 s); closing resets it.
- FR-CB-4. After the open period, up to `Breaker.HalfOpenProbes` (default 1) concurrent foreground fetches are admitted as probes. One success closes the breaker. One failure reopens it.
- FR-CB-5. While open, no fetch reaches the origin. Foreground requests get stale per FR-STL-2 (reason `circuit-open`) or `ErrCircuitOpen`. Background refreshes are dropped. Unsafe methods get `ErrCircuitOpen`.
- FR-CB-6. Every state transition emits an event and a log line.

### 5.12 Invalidation (RFC 9111 §4.4, RFC 9875)

- FR-INV-1. A 2xx or 3xx response to an unsafe or unknown method invalidates the target URI, and the URIs in `Location` and `Content-Location` when they are same-origin (same scheme, host and port) as the target. A URI is scheme, normalized host, path and rewritten query; invalidating it reaches every stored variant and every keyed-header or keyed-cookie partition of that URI.
- FR-INV-2. Unless `CacheGroups.Ignore` is set, a 2xx/3xx response to an unsafe method carrying `Cache-Group-Invalidation` soft-purges every stored response sharing a listed group on the same origin (RFC 9875 makes grouped invalidation a MAY; Weir applies it in the soft form so that one request cannot force blocking revalidation of a whole group, T-28). Responses to safe methods have their `Cache-Group-Invalidation` ignored. Grouped invalidation does not cascade.
- FR-INV-3. Target-URI, `Location` and `Content-Location` invalidation (FR-INV-1) marks entries invalid (validation required, never served stale). No invalidation deletes synchronously or iterates the store.

### 5.13 Purge (T6.12, T6.13)

- FR-PRG-1. `Purge` accepts any combination of `All`, `URLs` (absolute URLs, validated and rewritten exactly like requests) and `Groups`, and a `Mode` of `PurgeSoft` (default) or `PurgeHard`. `Groups` requires `Origin` (`scheme://host[:port]`) because RFC 9875 groups are scoped to an origin. Any invalid input rejects the whole call before any epoch is written.
- FR-PRG-2. Soft purge makes matching entries stale as of the purge time: an entry's expiry becomes the earlier of its original expiry and the purge time. Its SWR and SIE windows are measured from that expiry, and it is revalidated with conditional requests.
- FR-PRG-3. Hard purge makes matching entries unusable: treated as a miss, no stale serving, no conditional revalidation.
- FR-PRG-4. Purge cost is O(number of tags given), independent of how many entries match. Entries are evaluated lazily at lookup (epoch model, [05-storage-interface-spec.md §4](05-storage-interface-spec.md)).
- FR-PRG-5. `All` purges by bumping the global epoch. This is the generation mechanism for T6.13.
- FR-PRG-6. Group names are opaque strings compared byte-for-byte, scoped to origin (scheme, host, port) per RFC 9875.
- FR-PRG-7. An epoch applies to an entry when the entry's request time (the moment the request that produced it was sent) is not after the epoch. A fetch that started before a purge and finished after it therefore does not survive the purge.

### 5.14 Negative caching (T6.10)

- FR-NEG-1. When a foreground fetch ends in an origin-health failure and no stored response exists for the coalescing key (servable or not; a negative entry must never overwrite a response that could later be revalidated), Weir records a negative entry under the coalescing key for `Negative.TTL` (default 2 s). Disabled by `Negative.Disable`.
- FR-NEG-2. A negative entry holds only a status (502, 503 or 504) and an optional `Retry-After`. It never holds the origin's body or headers, so it never stores anything the origin marked `no-store`.
- FR-NEG-3. Requests that find a live negative entry and no servable stale entry get a synthesized response with that status, `Cache-Status: Weir; hit; detail=negative`, and no origin contact.
- FR-NEG-4. Negative entries are never created for 500, 4xx, bypassed requests, range requests, unsafe methods, or requests that carried `Authorization` (T-31).

### 5.15 Miss-rate signal (T6.8)

- FR-MR-1. Weir tracks requests and misses per partition in a Space-Saving summary of `MissRate.TopK` (default 64) counters per `MissRate.Window` (default 10 s). Memory is O(TopK).
- FR-MR-2. At the end of each window, a partition with at least `MissRate.MinMisses` (default 500) misses and a miss ratio of at least `MissRate.MinRatio` (default 0.9) raises a `miss_rate_anomaly` event and a warning log with the partition string truncated to 256 bytes.
- FR-MR-3. With `MissRate.Throttle` true (default false), an anomalous partition's limiter cap is set to 1 for the following window.

### 5.16 Warm start (T6.4)

- FR-WRM-1. `Warm` runs each request through the lookup and fetch path at background priority with `Warm.Concurrency` (default 4) concurrent fetches. Unlike background refresh, warm fetches wait for a slot (they do not drop), but they never use the foreground reserve.
- FR-WRM-2. `Warm` skips requests that already have a fresh entry and returns counts of fetched, skipped, not-stored and failed requests.

### 5.17 Storage failure (T6.5)

- FR-STF-1. Every call to a remote store (`Info().Remote` true) is bounded by `Timeouts.Store` (default 50 ms). A timeout counts as `ErrUnavailable`. In-process stores are called with the request context only, so the hit path does not allocate a timer.
- FR-STF-2. A store breaker opens after 5 consecutive `ErrUnavailable` results and stays open for 1 s (doubling to 30 s). While it is open, Weir skips store calls and treats lookups as misses.
- FR-STF-3. Store unavailability never bypasses coalescing, the limiter or the origin breaker.
- FR-STF-4. `ErrNotFound` is a normal miss and never counts toward the store breaker.

### 5.18 Observability (seed §7.5)

- FR-OBS-1. `Config.Observer`, if set, receives one `Event` per request outcome and one per notable internal event (fetch start and end, coalesce join, shed, stale served, breaker transition, store error, key rejection, not stored, vary overflow, eviction batch, purge, miss-rate anomaly). The full catalog is in [04-lld.md §9](04-lld.md).
- FR-OBS-2. Observers are called synchronously, from many goroutines at once (request goroutines and flight goroutines), and MUST be safe for concurrent use and MUST NOT block. Weir does not recover observer panics.
- FR-OBS-3. `Config.Logger` (`*slog.Logger`, default discard) receives state transitions and warnings only, never one line per request.
- FR-OBS-4. Prometheus and OpenTelemetry exporters are separate modules that implement `Observer`.

### 5.19 Lifecycle

- FR-LCY-1. `New` MUST reject invalid configuration: negative sizes, `Storable.MaxObjectBytes` larger than the store can admit (checked when the store implements `MaxObjectBytes() int64`, as the memory store does), `LeaderMaxAge` or `FollowerMaxWait` above `Timeouts.Origin`, `Jitter` outside [0, 0.5], `FailureRatio` outside (0, 1], or a `Storable.Statuses` list containing 206, 304, 500, 502, 503 or 504 (those statuses have dedicated handling and are never stored as entries).
- FR-LCY-2. After `Close` returns, no goroutine started by the engine is running.
- FR-LCY-3. The engine is safe for concurrent use by any number of goroutines.

## 6. Configuration

All fields are optional. The zero value of `Config` is valid and yields the defaults below.

| Field | Default | Notes |
|---|---|---|
| `Store` | memory store, 16 shards (halved, down to 1, while a shard's small queue could not hold `Storable.MaxObjectBytes`, so the zero Config stays valid under a small `GOMEMLIMIT`), size per D35: 40% of `GOMEMLIMIT` clamped to [16 MiB, 8 GiB], or 256 MiB with a warning when `GOMEMLIMIT` is unset | engine closes it on `Close` only if it created it |
| `Key.QueryDrop`, `Key.QueryKeep` | empty | patterns, trailing `*` allowed |
| `Key.QuerySort` | false | |
| `Key.NormalizePath` | false | |
| `Key.Headers`, `Key.Cookies` | empty | deny by default |
| `Key.Vary` | `VaryAuto` | or `VaryStrict` |
| `Key.VaryAllow` | empty | required for sensitive Vary names |
| `Key.MaxVaryHeaders` / `Key.MaxVariants` | 8 / 8 | |
| `Key.AcceptEncoding` | `["gzip"]` | set to what the origin produces, e.g. `["br","gzip"]` |
| `Forward.Mode` | `ForwardStrict` | `ForwardAll` logs a warning |
| `Forward.Allow` | empty | operator additions, on top of the trace defaults |
| `Forward.NoTraceHeaders` | false | D29: true stops forwarding `traceparent`, `tracestate`, `X-Request-Id` (a boolean, because nil-versus-empty slices do not survive JSON or Caddyfile round trips) |
| `Bypass.Cookies`, `Bypass.Headers` | empty | |
| `Storable.Statuses` | 200, 203, 204, 300, 301, 302, 307, 308, 404, 405, 410, 414, 501 | 302/307 need explicit freshness (D39) |
| `Storable.MaxObjectBytes` | 1 MiB | body plus headers |
| `Storable.StripSetCookie` | false | |
| `Freshness.Jitter` | 0.10 | range [0, 0.5] |
| `Freshness.JitterMinLifetime` | 10 s | |
| `Freshness.NoJitter`, `NoEarlyRefresh` | false | |
| `Freshness.EarlyRefreshBeta` | 1.0 | |
| `Freshness.HeuristicFraction` / `HeuristicMax` | 0.1 / 1 h | |
| `Freshness.DefaultTTL` | 0 | |
| `Freshness.DefaultStaleWhileRevalidate` / `DefaultStaleIfError` | 0 / 0 | decision D6 |
| `Freshness.Keep` | 5 min | extra retention after the last stale window, only for entries with a validator, so an expired entry can still be revalidated with a cheap 304 instead of refetched (the memory store evicts it earlier under pressure) |
| `Coalesce.LeaderMaxAge` / `FollowerMaxWait` | 10 s / 10 s | a default is lowered to `Timeouts.Origin` when that is shorter; an explicit value above it is rejected (FR-LCY-1) |
| `Coalesce.HitForMissTTL` | 30 s | |
| `Limiter.MaxConcurrent` / `MaxQueue` / `MaxQueueWait` | 64 / 1024 / 2 s | |
| `Limiter.MaxPerPartition` | 16 | |
| `Limiter.ReserveForeground` | 25% of `MaxConcurrent` | |
| `Breaker.Window` / `MinRequests` / `FailureRatio` | 10 s / 20 / 0.5 | |
| `Breaker.OpenFor` / `MaxOpenFor` / `HalfOpenProbes` | 5 s / 60 s / 1 | |
| `Breaker.CountStatus500` | false | |
| `Breaker.Disable` | false | |
| `Negative.TTL` | 2 s | `Negative.Disable` turns it off |
| `MissRate.Window` / `TopK` / `MinMisses` / `MinRatio` | 10 s / 64 / 500 / 0.9 | |
| `MissRate.Throttle` | false | |
| `MissRate.Disable` | false | |
| `CacheGroups.Ignore` | false | RFC 9875 honored by default |
| `Client.HonorRevalidation` | false | decision D5 |
| `Timeouts.Origin` / `Background` / `Store` | 30 s / 30 s / 50 ms | |
| `Warm.Concurrency` | 4 | |
| `Limiter.MaxPerHost` | 0 (off) | M14; the Caddy adapter sets 25% for multi-host sites |
| `Limiter.MaxUpload` | 25% of `MaxConcurrent` | D25, separate pool for requests with a body |
| `Timeouts.StreamIdle` | 60 s | D26 |
| `Bypass.ReportStrippedCookies` | 5 min | D31; a negative value disables |
| `Limits.*` | see FR-VAL-1, FR-VAL-3, FR-STO-10 | |
| `CacheStatus` | `"Weir"` | member name |
| `NoCacheStatus` | false | true omits the header |
| `Observer` | nil | |
| `Logger` | discard handler | |
| `Rand` | `rand.Float64` from `math/rand/v2` | test hook; must return [0, 1) and be safe for concurrent use |

## 7. Decision tables

### 7.1 Lookup outcome

Evaluated in order after validation, bypass check, and store lookup.

| Condition | Action | Cache-Status |
|---|---|---|
| unsafe or unknown method | forward (FR-FWD-3), invalidate on 2xx/3xx | `fwd=method` |
| bypass rule matches | forward, do not store | `fwd=bypass` |
| request has `Range` and no fresh or SWR-servable entry | forward with Range, do not store | `fwd=uri-miss`, `fwd=vary-miss` or `fwd=stale` |
| no stored response; negative entry live | synthesized error | `hit; detail=negative` |
| no entry, hit-for-miss marker | fetch without coalescing | `fwd=uri-miss; detail=hit-for-miss` |
| no entry (or vary miss) | coalesced fetch | `fwd=uri-miss` / `fwd=vary-miss` |
| fresh, no `no-cache` | serve; maybe early refresh | `hit; ttl=N` |
| fresh with `no-cache`, or stale outside SWR, or soft-purged outside SWR, or invalidated | coalesced validation | `fwd=stale; fwd-status=304` on success |
| stale within SWR | serve stale, background refresh | `hit; ttl=-N; detail=stale-while-revalidate` |

### 7.2 On fetch failure

| Failure | Stale within SIE and permitted | Otherwise |
|---|---|---|
| transport error | serve stale | `ErrOrigin` (502), negative entry |
| origin timeout | serve stale | `ErrOriginTimeout` (504), negative entry |
| 502/503/504 response | serve stale | pass the origin response through, negative entry |
| 500 response | serve stale | pass the response through, no negative entry |
| shed | serve stale | `ErrShed` (503) |
| breaker open | serve stale | `ErrCircuitOpen` (503) |
| follower wait expired | serve stale | fetch independently (FR-COA-4) |
| any of the above on a `must-revalidate` or `proxy-revalidate` entry | not permitted | `ErrMustRevalidate` (504), except a 500/502/503/504 response is passed through |

"Negative entry" in this table means: written only when no stored response exists for the key and the request carried no `Authorization` (FR-NEG-1, FR-NEG-4).

## 8. RFC conformance summary

| Reference | Requirement | Weir |
|---|---|---|
| 9111 §3 | storage conditions | FR-STO-1..10, stricter status set by default |
| 9111 §3.1 | stored header exclusions | FR-STO-11 |
| 9111 §3.3–3.4 | incomplete and partial responses | never stored |
| 9111 §3.5 | Authorization | FR-STO-5 |
| 9111 §4 | Age on reuse; unsafe methods written through; collapsed requests | FR-FRS-7, FR-FWD-3, FR-COA-5 |
| 9111 §4.1 | Vary matching, `Vary: *` | FR-KEY-7..11 |
| 9111 §4.2.1–4.2.3 | lifetime, heuristic, age | FR-FRS-1..4 |
| 9111 §4.2.4 | stale serving only when permitted | FR-STL-1..5, D6 |
| 9111 §4.3 | validation and freshening | FR-SRV-2, FR-SRV-3 |
| 9111 §4.3.5 | freshening with HEAD | not implemented (HEAD misses are fetched as GET) |
| 9111 §4.4 | invalidation | FR-INV-1, FR-INV-3 |
| 9111 §5.2.1 | request directives (advisory) | `no-store` and `only-if-cached` honored; others per D5 |
| 9111 §5.2.2 | response directives | all honored; qualified `no-cache` and `private` treated as unqualified |
| 9111 §5.2 | proxies pass cache directives through | FR-FWD-1 |
| 5861 | stale-while-revalidate, stale-if-error | FR-STL-1, FR-STL-2, FR-STL-6 |
| 9110 §7.6.1 | hop-by-hop fields | FR-FWD-2, FR-FWD-7, FR-STO-11 |
| 9110 §12.5.3 | Accept-Encoding | §5.2.3 |
| 9110 §13.2.2 | precondition evaluation order | FR-SRV-2 |
| 9211 | Cache-Status | FR-SRV-9 |
| 9875 | Cache-Groups, Cache-Group-Invalidation | FR-STO-10, FR-INV-2, FR-PRG-6 |
| 9213 | targeted cache-control | not in Phase 1 |

## 9. Non-functional requirements

- NFR-1. Correctness under `go test -race` for every package, always.
- NFR-2. No panics from any request, response, or configuration value that passes `New`. Enforced by fuzz targets for every parser.
- NFR-3. Every in-memory structure has a stated bound: store bytes (configured capacity), flights (≤ `MaxConcurrent + MaxQueue`), limiter waiters (≤ `MaxQueue`), miss-rate counters (`TopK`), epochs (fixed-size sketch for soft and invalid epochs, 4 MiB by default, plus a capped exact table for hard purges, [05 §4.4](05-storage-interface-spec.md)), variants per primary key (`MaxVariants`).
- NFR-4. Transient body memory is bounded by `MaxConcurrent × MaxObjectBytes` (64 MiB at defaults).
- NFR-5. Hit path budget on the reference machine (Apple M-series, Go 1.27): `BenchmarkServeHitSmall` at most 4 µs/op and 16 allocs/op. These numbers are provisional until the first measurement in M10; after that a regression over 20% fails review.
- NFR-6. Zero third-party imports in the root module, checked in CI by `go list -deps`.
- NFR-7. Public API documented with godoc on every exported identifier; `go vet` and `golangci-lint` clean.

## 10. Failure-mode traceability

| Seed mode | Requirements | Milestone | Primary tests ([07-testing-strategy.md](07-testing-strategy.md)) |
|---|---|---|---|
| T6.1 synchronized expiry | FR-FRS-5, FR-FRS-6 | M3 | `TestJitterSpread`, `TestJitterNeverLengthens`, `TestEarlyRefreshProbability` |
| T6.2 hot key | FR-COA-1..3 | M2 | `TestCoalesceColdKey1000` |
| T6.2a lock starvation | FR-COA-3, FR-COA-4 | M2 | `TestCoalesceStuckLeader`, `TestCoalesceLeaderAging` |
| T6.3 cross-key stampede | FR-LIM-1..6 | M4 | `TestLimiterCap5000Keys`, `TestPartitionFairness` |
| T6.4 cold start | FR-LIM-*, FR-WRM-* | M4 | `TestColdStartBounded`, `TestWarm` |
| T6.5 storage outage | FR-STF-*, FR-COA-7 | M4 | `TestStoreOutageStillCoalescedAndLimited` |
| T6.6 origin outage | FR-STL-*, FR-CB-* | M5 | `TestStaleIfErrorOnOriginDown`, `TestBreakerOpensHalfOpenCloses`, `TestBreaker500DoesNotTrip` |
| T6.7 unkeyed inputs | FR-KEY-*, FR-FWD-* | M1, M7 | `TestForwardEqualsKey`, `TestVaryUnconfiguredHeader`, `TestKettleUserAgent` |
| T6.8 cache busting | FR-LIM-3, FR-MR-* | M4, M8 | `TestRandomQueryFloodBounded`, `TestMissRateAnomaly` |
| T6.9 malformed input | FR-VAL-*, §5.2.3 | M1, M7 | `FuzzAcceptEncoding`, `FuzzKeyEncodingInjective`, `TestCVE202435296` |
| T6.10 negative caching | FR-NEG-* | M6 | `TestNegativeCacheBurst` |
| T6.11 eviction storms | [05-storage-interface-spec.md §5](05-storage-interface-spec.md) | M1 (store), 2.5 | `TestS3FIFOScanResistance`, `TestOneHitWondersDoNotEvictHot` |
| T6.12 purge herd | FR-PRG-* | M9 | `TestSoftPurgeServesStaleWhileRevalidating`, `TestPurge5000KeysBounded` |
| T6.13 rolling deploy | FR-PRG-5 | M9 | `TestGlobalEpochSoft` |

## 11. Deviations from the seed

| Seed | This spec | Why |
|---|---|---|
| RFC 7234 | RFC 9111 and companions | 7234 was obsoleted in June 2022. |
| `Decide`/`Complete` API (§7.2) | `Serve(ctx, req, origin)` | D1. The split made background refresh, lock release and slot release depend on every adapter calling `Complete` correctly. |
| Jitter `base + random(0, window)` (§6.1) | `base × (1 − J·U)` | Lengthening an origin-declared lifetime serves stale content without permission (RFC 9111 §4.2.4). |
| Controllable `Clock` (§9 Phase 0) | `testing/synctest` | D9. The standard library now fakes time for the whole bubble, including timers inside the code under test, with no production indirection. |
| Sharded LRU with admission awareness (§6.11) | Sharded byte-weighted S3-FIFO | D2. S3-FIFO's small probationary queue is admission control; one-hit wonders from a busting flood never reach the main queue. |
| M1 without adversarial hardening | M1 includes injective key encoding, key-forward consistency and input limits | The seed's own §7.3 says the key is a security boundary "from the first line of code". Retrofitting in M7 would mean rewriting M1. |
| `KeyBuilder.Build(req, varyHeaders)` | primary key, vary spec, variant key | The `Vary` of a resource is unknown until a response is stored, so a one-step key cannot include it. |
| `Store.Set(..., ttl)` | retention = lifetime + max stale window + `Keep`; plus tag epochs | Stored entries must outlive freshness to support SWR, SIE and 304 revalidation. |
| Soft purge by marking entries | Lazy epoch per tag | O(1) purges with no scan, and the same mechanism gives T6.13 generations. |
| Breaker on N consecutive failures | Failure ratio with minimum volume, gateway failures only | An attacker who can trigger 500s (or one bad endpoint) could otherwise open the breaker for the whole origin. |
| Negative-cache the 5xx response | Synthesized status-only negative entry, gateway failures only | Avoids storing bodies the origin marked `no-store`, and avoids caching input-dependent 500s. |
| Global bulkhead | Global cap plus per-partition cap and a foreground reserve | A global cap alone protects the origin but lets one flooded path consume every slot. |
| Miss-rate tracking only for configured excluded params | Always on, per partition, Space-Saving top-K | Cheap, bounded, and attackers do not follow the operator's parameter list. |
| "Optionally a staleness header" | RFC 9211 `Cache-Status` | A standard exists. |
| Surrogate keys | RFC 9875 `Cache-Groups` plus `Purge` API | Published as a Proposed Standard in October 2025. |
| Warm-start interface only | `Warm` implemented as request replay; snapshots deferred | The replay path is the normal fetch path at background priority, a few dozen lines. |

## 12. Open questions

Resolved on 2026-09-27:

- OQ-1 single-range 206: D11, §13.1.
- OQ-2 targeted fields: D12, §13.2.
- OQ-3 adaptive limits: D13. Static in Phase 1; revisit with load-test data. The limiter reads its cap through one method so an adaptive policy can replace it.
- OQ-4 experiment bucketing: decisions E1 to E7 in [10-experiments-spec.md](10-experiments-spec.md).

Open: none that block any milestone before Phase 3.

## 13. Phase 1.x requirements (M11 to M15)

### 13.1 Single-range responses (M11, D11)

- FR-RNG-1. For a `GET` with `Range` whose stored entry is fresh or servable under SWR and has status 200, Weir evaluates `If-Range` first (RFC 9110 §13.1.5: only a strong `ETag` match, or a `Last-Modified` date that is a strong validator, lets the range apply; otherwise the full 200 is sent).
- FR-RNG-2. A single `bytes` range (`a-b`, `a-`, `-n`) that is satisfiable is answered with 206, `Content-Range: bytes a-b/len`, and a body sliced from the stored bytes without copying. The range applies to the stored representation bytes (after any content coding).
- FR-RNG-3. An unsatisfiable single range yields 416 with `Content-Range: bytes */len`. Invalid specifiers, unknown units, and requests with more than one range get the full 200 (RFC 9110 §14.2 permits ignoring Range). `HEAD` ignores `Range`.
- FR-RNG-4. A range request with no usable entry is passed through as in FR-SRV-5. If the origin answers 206 with `Content-Range: bytes a-b/total`, `total <= Storable.MaxObjectBytes`, and the 206 would pass every storability rule except its status, Weir starts one background full-object fetch for the key (coalesced, background class) so later range requests are served from cache. Range requests never trigger foreground full fetches (T-37).
- FR-RNG-5. Responses produced by FR-RNG-2 and FR-RNG-3 carry `Cache-Status: Weir; hit` (RFC 9211 §2.1 counts 206 built from a stored response as a hit).

### 13.2 Targeted cache-control fields (M12, D12)

- FR-TCC-1. Target list, in priority order: `Weir-Cache-Control`, `CDN-Cache-Control`. Each is parsed as an RFC 9651 Dictionary. An empty or unparsable field is ignored (RFC 9213 §2.1). Parser: `internal/sfv`, fuzzed.
- FR-TCC-2. The first valid field on the list determines lifetime, stale windows, `must-revalidate`, `proxy-revalidate`, `s-maxage` and `public` for this cache, and `Cache-Control` freshness directives and `Expires` are ignored.
- FR-TCC-3. Deviation from RFC 9213, toward caching less: `private`, `no-store` and `no-cache` found in either the targeted field or `Cache-Control` all apply. A framework that sets `CDN-Cache-Control` globally but marks per-user pages `private` only in `Cache-Control` therefore does not leak them (T-34).
- FR-TCC-4. `Weir-Cache-Control` is removed from responses sent to clients and kept in stored headers (needed to recompute freshness after 304s). `CDN-Cache-Control` is passed through unless an experiment rewrite applies ([10 §2](10-experiments-spec.md) E7).
- FR-TCC-5. Weir does not rewrite `Age` or `Date` to hide the longer targeted lifetime from downstream caches (RFC 9213 §2.3 "age penalty"); downstream browsers revalidate against Weir, which answers 304 cheaply. Documented for operators.

### 13.3 Snapshots (M13, D15)

- FR-SNP-1. `memory.Config.SnapshotPath`, when set, makes the memory store write a snapshot when it is closed: every live response and vary-spec record plus all hard epochs, in the store codec with a per-record CRC-32C, to a temporary file in the same directory created with mode 0600, then `fsync` and atomic rename. Markers, negative entries and the soft/invalid sketch are not written. Writing stops at the `Close` deadline; an incomplete snapshot is discarded, never renamed.
- FR-SNP-2. `memory.New` with `SnapshotPath` loads an existing snapshot: records failing CRC or decoding are skipped and counted, records past `Expires` are dropped, hard epochs are restored, and then the store writes a global soft epoch at load time, so every loaded entry is stale as of the restart (T-33). The file is removed after a successful load so a crash later cannot reload old content.
- FR-SNP-3. Load respects `MaxBytes` and per-owner quotas; records beyond capacity are dropped in file order (the writer emits main-queue records before small-queue records so hot entries load first).

### 13.4 Per-host fairness (M14, D16)

- FR-FAIR-1. `Limiter.MaxPerHost` caps in-flight origin fetches per normalized host, in addition to the partition cap. 0 (library default) disables it. Memory stays O(`MaxConcurrent`).
- FR-FAIR-2. `store.Entry.Owner` is an opaque `Tag` the engine sets to the entry's origin tag. `memory.Config.MaxBytesPerOwner` (0 disables; library default 0) caps bytes per owner per shard. A `Set` that would push its owner over the cap first evicts that owner's own entries, scanning at most 64 nodes from the small-queue tail and then the main-queue tail for same-owner victims; if that frees too little, the `Set` is declined (S-4). A tenant can therefore turn over its own quota, but can never evict another tenant's entries (T-32). Declining outright was rejected: a legitimate tenant whose working set exceeds its quota would keep its oldest entries for up to `MaxRetention` while its new pages went uncached.
- FR-FAIR-3. The Caddy adapter enables both at 25% (of `MaxConcurrent` and of shard bytes) when a site serves more than one host or uses on-demand TLS, unless configured otherwise.

### 13.5 Eager hard purge (M15, D18)

- FR-PRG-8. `Purge{Mode: PurgeHard, Eager: true}` also deletes matching records immediately when the store implements the optional `store.Scrubber` interface (`Scrub(ctx, tags []Tag) (int, error)`). The memory store scans one shard at a time under its lock and deletes response records whose tags intersect, which reaches every variant and every keyed-header partition. The epoch is still written first, so reachability never depends on the scan finishing. `Eager` with `PurgeSoft` is an error. Stores without `Scrubber` return `ErrEagerUnsupported` after the epoch is written.

## 14. Request, stream and operations requirements (decisions D25 to D42)

### 14.1 Upload pool (D25)

- FR-LIM-7. A request whose body is non-empty or of unknown length (any method) takes its slot from a separate upload pool of `Limiter.MaxUpload` slots with its own queue rules, never from the main pool. Bodyless requests, including bypassed `GET`s from logged-in users, use the main pool. Total origin concurrency is therefore bounded by `MaxConcurrent + MaxUpload`. A slow uploader can exhaust only the upload pool (T-39). Adapters MUST enforce a request body size limit and a body read timeout (Caddy `request_body max_size` and server `read_body` timeout; `http.Server.ReadTimeout` for weirhttp); the adapter docs say so.

### 14.2 Timeouts (D26)

- FR-TMO-1. `Timeouts.Origin` bounds the time from sending the request to receiving response headers, and, for buffered bodies (at most `MaxObjectBytes`), the whole body read. A drip-feeding origin therefore cannot hold a limiter slot beyond it (T-41).
- FR-TMO-2. Streamed bodies (pass-through, oversized) have no total deadline. Each read that makes no progress for `Timeouts.StreamIdle` (default 60 s) fails the stream. Their limiter slot was already released at headers (FR-LIM-1).

### 14.3 Upgrades and CONNECT (D27)

- FR-UPG-1. `Serve` returns `ErrUpgradeNotSupported` (status 501 via `StatusCode`, but adapters should never get there) for requests with method `CONNECT` (which also covers HTTP/2 and HTTP/3 extended CONNECT WebSockets, RFC 8441 and RFC 9220) or with `Connection: upgrade` and an `Upgrade` field, except an `Upgrade` whose only token is `h2c` (any case). RFC 9110 §7.8 lets a server ignore `Upgrade`, so that request is served, keyed and forwarded like a plain one; `Upgrade` and `HTTP2-Settings` are hop-by-hop and never reach the origin (FR-FWD-2). `h2c` next to any other token is still an upgrade. `weirhttp` and the Caddy adapter detect these first and hand them to the next handler directly, outside the limiter.

### 14.4 Event streams (D28)

- FR-STR-1. A response with `Content-Type: text/event-stream` (or a type in `Storable.StreamTypes`) is streamed to the requester immediately: never buffered, never stored, never shared with followers (followers re-enter per FR-COA-5), no marker.

### 14.5 Trace headers (D29)

- FR-FWD-6. Unless `Forward.NoTraceHeaders` is set, fetches forward `traceparent`, `tracestate` and `X-Request-Id` in addition to `Forward.Allow`. `traceparent` must match the W3C Trace Context version-00 format (`00-<32 hex>-<16 hex>-<2 hex>`, not all-zero ids) or it is dropped together with `tracestate`. `tracestate` combined across its lines longer than 512 bytes (the W3C limit) or containing a byte outside 0x21–0x7E is dropped on its own, `traceparent` kept. `X-Request-Id` longer than 128 bytes or containing bytes outside 0x21–0x7E is dropped. Coalesced followers' trace headers are not forwarded (only the flight creator's reach the origin). Echoing these into cacheable bodies is an origin bug, documented as T-40.

### 14.6 Stripped-cookie report (D31)

- FR-OBS-5. For `Bypass.ReportStrippedCookies` after `New` (default 5 minutes), Weir counts cookie names (never values) stripped by strict forwarding in a Space-Saving summary of 32 names, then logs the top names once and stops. Names are logged only if they match the RFC 6265 token grammar.

### 14.7 Incident modes (D33)

- FR-MODE-1. `Engine.SetMode(m Mode, ttl time.Duration) error` switches between `ModeNormal`, `ModeStaleOnError` and `ModeBypass`. `ttl` is required, at most 24 h; the mode reverts to normal when it expires. Modes are not persisted across restarts. Every change emits `EvMode` and a log line.
- FR-MODE-2. `ModeStaleOnError`: on any error condition (FR-STL-2), a stored entry may be served stale even without an SIE window, up to 24 h of staleness. It still never serves entries that are hard-purged, invalidated, or marked `must-revalidate`, `proxy-revalidate` or `no-cache` (RFC 9111 §4.2.4 allows stale when disconnected but not against those directives).
- FR-MODE-3. `ModeBypass`: every request is handled as pass-through (FR-FWD-3), still through the limiter and breaker, never stored. Existing entries are untouched.

### 14.8 Memory sizing (D35)

- FR-MEM-1. With `Config.Store` nil, the memory store size is 40% of `debug.SetMemoryLimit(-1)` when that is below `math.MaxInt64`, clamped to [16 MiB, 8 GiB]; otherwise 256 MiB and a startup warning recommending `GOMEMLIMIT`. The Caddy adapter divides the 40% budget evenly across named stores that do not set `max_bytes`, so several sites in one process cannot overcommit (T-43).

