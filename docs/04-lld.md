# Weir low-level design

Status: v1.0
Date: 2026-09-28
Depends on: [01-technical-spec.md](01-technical-spec.md), [02-architecture.md](02-architecture.md), [03-hld.md](03-hld.md)

This document is written for the person (or agent) implementing a milestone. It gives exact type definitions, algorithms, locking rules and pseudo-code. Code may differ in naming of unexported identifiers; exported names, behavior, bounds and locking rules may not change without updating this document in the same commit.

Pseudo-code is Go-flavored and omits error plumbing where it adds nothing.

## 1. Public types (package `weir`)

### 1.1 Config

```go
type Config struct {
	Store         store.Store // nil: memory store with memory.Config{} defaults
	Key           KeyConfig
	Forward       ForwardConfig
	Bypass        BypassConfig
	Storable      StorableConfig
	Freshness     FreshnessConfig
	Coalesce      CoalesceConfig
	Limiter       LimiterConfig
	Breaker       BreakerConfig
	Negative      NegativeConfig
	MissRate      MissRateConfig
	CacheGroups   CacheGroupsConfig
	Client        ClientConfig
	Timeouts      TimeoutsConfig
	Warm          WarmConfig
	Limits        LimitsConfig
	CacheStatus   string // Cache-Status member name; "": "Weir"
	NoCacheStatus bool   // omit the Cache-Status header (T-27)
	Observer      Observer
	Logger        *slog.Logger
	Rand          func() float64 // [0,1); must be safe for concurrent use; default rand.Float64 (math/rand/v2)
}

type KeyConfig struct {
	QueryDrop      []string // exact names or "prefix*"
	QueryKeep      []string // allowlist; empty means keep all
	QuerySort      bool
	NormalizePath  bool
	Headers        []string // canonicalized with http.CanonicalHeaderKey at New
	Cookies        []string
	Vary           VaryMode // VaryAuto (zero value) or VaryStrict
	VaryAllow      []string
	MaxVaryHeaders int      // 0: 8
	MaxVariants    int      // 0: 8
	AcceptEncoding []string // 0-len: ["gzip"]; lowercase tokens
}

type ForwardConfig struct {
	Mode           ForwardMode // ForwardStrict (zero value) or ForwardAll
	Allow          []string
	NoTraceHeaders bool // D29, FR-FWD-6
}

type BypassConfig struct {
	Cookies               []string
	Headers               []string
	ReportStrippedCookies time.Duration // FR-OBS-5; 0: 5m; negative disables
}

type StorableConfig struct {
	Statuses       []int // nil: default set
	MaxObjectBytes int64 // 0: 1 MiB
	StripSetCookie bool
}

type FreshnessConfig struct {
	Jitter                      float64 // 0: 0.10 unless NoJitter
	NoJitter                    bool
	JitterMinLifetime           time.Duration // 0: 10s
	EarlyRefreshBeta            float64       // 0: 1.0
	NoEarlyRefresh              bool
	HeuristicFraction           float64       // 0: 0.1
	HeuristicMax                time.Duration // 0: 1h
	DefaultTTL                  time.Duration // 0: none
	DefaultStaleWhileRevalidate time.Duration // 0: none
	DefaultStaleIfError         time.Duration // 0: none
	Keep                        time.Duration // 0: 5m; applies only to entries with a validator
}

type CoalesceConfig struct {
	LeaderMaxAge    time.Duration // 0: min(10s, Timeouts.Origin)
	FollowerMaxWait time.Duration // 0: min(10s, Timeouts.Origin)
	HitForMissTTL   time.Duration // 0: 30s
}

type LimiterConfig struct {
	MaxConcurrent     int           // 0: 64
	MaxQueue          int           // 0: 1024
	MaxQueueWait      time.Duration // 0: 2s
	MaxPerPartition   int           // 0: 16 (clamped to MaxConcurrent)
	ReserveForeground int           // 0: MaxConcurrent/4 (at least 1 when MaxConcurrent >= 2)
	MaxPerHost        int           // 0: off (M14)
	MaxUpload         int           // D25; 0: MaxConcurrent/4, at least 1
}

type BreakerConfig struct {
	Window         time.Duration // 0: 10s (10 buckets)
	MinRequests    int           // 0: 20
	FailureRatio   float64       // 0: 0.5
	OpenFor        time.Duration // 0: 5s
	MaxOpenFor     time.Duration // 0: 60s
	HalfOpenProbes int           // 0: 1
	CountStatus500 bool
	Disable        bool
}

type NegativeConfig struct {
	TTL     time.Duration // 0: 2s
	Disable bool
}

type MissRateConfig struct {
	Window    time.Duration // 0: 10s
	TopK      int           // 0: 64
	MinMisses int           // 0: 500
	MinRatio  float64       // 0: 0.9
	Throttle  bool
	Disable   bool
}

type CacheGroupsConfig struct{ Ignore bool } // zero value honors RFC 9875
type ClientConfig struct{ HonorRevalidation bool }
type WarmConfig struct{ Concurrency int } // 0: 4

type TimeoutsConfig struct {
	Origin     time.Duration // 0: 30s
	Background time.Duration // 0: 30s
	Store      time.Duration // 0: 50ms (remote stores only, see §5.2)
	StreamIdle time.Duration // 0: 60s (D26, FR-TMO-2)
}

type LimitsConfig struct {
	MaxPathBytes        int // 0: 8192
	MaxQueryBytes       int // 0: 8192
	MaxQueryParams      int // 0: 256
	MaxKeyedHeaderBytes int // 0: 1024
	MaxGroups           int // 0: 32
	MaxGroupBytes       int // 0: 128
}
```

Note: the spec's `CacheGroups.Honor` default of true is expressed as a zero-value `Ignore` flag so the zero `Config` is correct. Every boolean in `Config` is written so that `false` is the default.

`New` copies the config, applies defaults, canonicalizes header names (and lowercases `Key.AcceptEncoding`), drops repeated names keeping the first, rejects header, cookie and coding names that are not RFC 9110 tokens (T-3, T-13), compiles query patterns, sorts nothing that the user ordered (for example `Key.Cookies` order is the forwarded order), and validates per FR-LCY-1. It returns `fmt.Errorf("%w: field %s: %s", ErrInvalidConfig, name, reason)`.

### 1.2 Request, Response, Purge, WarmStats

As in [01-technical-spec.md §4](01-technical-spec.md), plus:

```go
type Purge struct {
	Mode   PurgeMode // PurgeSoft (zero value) or PurgeHard
	All    bool
	URLs   []string  // absolute http(s) URLs; parsed and rewritten exactly like requests
	Origin string    // "scheme://host[:port]"; required when Groups is non-empty
	Groups []string
	Eager  bool      // M15: with PurgeHard, also delete matching records now (store.Scrubber)
}

type WarmStats struct{ Fetched, Skipped, NotStored, Failed int }

type EngineStats struct {
	Inflight     int // origin fetches holding limiter slots
	Queued       int // fetches waiting for a slot
	BreakerState BreakerState // Closed, HalfOpen, Open
	StoreBytes   int64 // -1 when the store does not implement Bytes() int64
}
```

`Purge` validates every URL with the same validator as `Serve` and returns `ErrInvalidRequest` for a bad one without writing any epoch. Purge is all-or-nothing at the validation stage; epoch writes are independent (a store failure mid-way returns the error and the epochs already written stay written, which errs toward purging).

### 1.3 Errors

```go
var (
	ErrInvalidConfig  = errors.New("weir: invalid config")
	ErrInvalidRequest = errors.New("weir: invalid request")
	ErrShed           = errors.New("weir: origin capacity exhausted")
	ErrCircuitOpen    = errors.New("weir: origin circuit open")
	ErrOriginTimeout  = errors.New("weir: origin timeout")
	ErrMustRevalidate = errors.New("weir: must-revalidate response could not be validated")
	ErrOnlyIfCached   = errors.New("weir: only-if-cached and no stored response")
	ErrOrigin         = errors.New("weir: origin error")
	ErrClosed         = errors.New("weir: engine closed")
	ErrUpgradeNotSupported = errors.New("weir: connect and protocol upgrades not supported") // FR-UPG-1
	ErrEagerUnsupported = errors.New("weir: store cannot scrub; epoch written, delete skipped") // M15
)

type RequestError struct{ Reason string } // Is(ErrInvalidRequest) == true
type OriginError struct{ Err error }       // Is(ErrOrigin) == true; Unwrap returns Err
```

`StatusCode`: `ErrInvalidRequest` 400; `ErrUpgradeNotSupported` 501; `ErrShed`, `ErrCircuitOpen`, `ErrClosed` 503; `ErrOriginTimeout`, `ErrMustRevalidate`, `ErrOnlyIfCached`, `context.DeadlineExceeded` 504; `ErrOrigin` 502; `context.Canceled` 499 (a hint for logs only; the client is gone and adapters should not write); anything else 502. The outermost recognized error in the chain decides, in `errors.Is` walk order, and an `*OriginError` maps to 502 whatever it wraps. An origin that returns weir errors (another engine used as origin) or its own `context` errors (an `http.Client` timeout matches `context.DeadlineExceeded`) cannot turn an origin failure into 400, 499, 503 or 504, and its `RetryError` hint is not returned by `RetryAfter`. So `timeoutOrOrigin(err, ctx, tctx)` (§6.7) decides from the contexts, never from `err`. When `ctx` is done, it returns `ctx.Err()` unwrapped for direct and pass-through fetches, whose `ctx` is the request context, and `ErrClosed` for flights and background fetches, whose context ends only on `Close` (§6.4). Otherwise, when `context.Cause(tctx)` is `ErrOriginTimeout`, it returns `ErrOriginTimeout`. In every other case it returns `*OriginError{err}`. A waiter on a flight cut short by `Close` therefore gets 503, not 499.

```go
type RetryError struct {
	Err   error         // ErrShed or ErrCircuitOpen
	After time.Duration // RetryAfter rounds it up to whole seconds, never negative
}
```

The engine wraps shed errors with `After = MaxQueueWait` and circuit-open errors with the breaker's remaining open time. `RetryAfter(err)` returns `After` only when a `*RetryError` directly wraps the `ErrShed` or `ErrCircuitOpen` that decided `StatusCode` under the chain rule above. `errors.Is(err, ErrShed)` still works through `Unwrap`.

## 2. Package `store`

Full contract in [05-storage-interface-spec.md](05-storage-interface-spec.md). Types:

```go
type Key [32]byte
type Tag [32]byte

type Kind uint8
const (
	KindResponse Kind = iota + 1
	KindVarySpec
	KindHitForMiss
	KindNegative
)

type Flags uint16
const (
	FlagMustRevalidate Flags = 1 << iota
	FlagProxyRevalidate
	FlagNoCache     // unqualified or qualified, treated the same
	FlagSMaxAge
	FlagHeuristic
	FlagPublic
	FlagFromAuthorized // stored from a request that carried Authorization
)

type Entry struct {
	Kind         Kind
	StoredAt     time.Time // keeps its monotonic reading in process; the codec strips it (FR-FRS-8)

	// KindResponse
	Status              int
	Header              http.Header // immutable after Set
	Body                []byte      // immutable after Set
	RequestTime         time.Time
	ResponseTime        time.Time
	Date                time.Time
	CorrectedInitialAge time.Duration
	Lifetime            time.Duration // jittered
	SWR, SIE            time.Duration // effective windows, 0 when not permitted
	Flags               Flags
	ETag                string
	LastModified        time.Time // zero when absent
	FetchDuration       time.Duration
	VaryNames           []string  // canonical names this variant was keyed on; nil when no Vary
	Tags                []Tag     // implicit tags + groups
	Owner               Tag       // origin tag; opaque to stores, used for per-owner quotas (M14)

	// KindVarySpec
	// VaryNames (shared field) plus:
	Variants []VariantRef // bounded by MaxVariants, copy-on-write

	// KindNegative
	// Status (shared field) plus:
	RetryAfter time.Duration

	// every kind
	Expires time.Time // absolute retention deadline; the store drops the record after this
}

type VariantRef struct {
	Key     Key
	Expires time.Time
}

type Epoch struct {
	At   time.Time
	Mode EpochMode // EpochSoft, EpochInvalid, EpochHard (ordered by severity)
}

type Info struct {
	Name   string
	Remote bool // true: the engine wraps calls in Timeouts.Store and the store breaker
}

type Store interface {
	Get(ctx context.Context, k Key) (*Entry, error)
	Set(ctx context.Context, k Key, e *Entry) error // retention is e.Expires
	Delete(ctx context.Context, k Key) error
	SetEpoch(ctx context.Context, t Tag, ep Epoch) error
	// NewestEpoch returns the most severe, then newest, epoch among tags whose
	// At >= since; ok=false when none qualifies.
	NewestEpoch(ctx context.Context, tags []Tag, since time.Time) (ep Epoch, ok bool, err error)
	Info() Info
	Close() error
}

var (
	ErrNotFound    = errors.New("store: not found")
	ErrUnavailable = errors.New("store: unavailable")
)
```

`Entry.Size()` returns `len(Body) + header bytes + vary name bytes + 32 per tag + 56 per variant ref + 256` (fixed overhead) and is what byte-weighted stores account. Vary specs have no body, so leaving out their names and refs would let up to `MaxVariants` refs per record go uncounted against the store's byte bound (NFR-3).

Tags are computed by `internal/keys`, except `TagGlobal`, which `store.TagGlobal()` defines so stores can recognize it (05 E-5) without importing `internal/keys`:

```
TagGlobal         = sha256("weir/tag/v1\x00global")
TagOrigin(o)      = sha256("weir/tag/v1\x01" + len-prefixed(o))
TagURI(o, p, q)   = sha256("weir/tag/v1\x02" + len-prefixed(o) + len-prefixed(p) + len-prefixed(q))
TagGroup(o, name) = sha256("weir/tag/v1\x03" + len-prefixed(o) + len-prefixed(name))
```

`o` is `scheme://host[:port]` after host normalization (default port removed).

## 3. `internal/keys`

### 3.1 Classification

```go
type Class uint8 // ClassCacheable, ClassPass

type Classified struct {
	Class      Class
	FwdReason  FwdReason // for ClassPass: FwdMethod or FwdBypass
	Head       bool      // client method was HEAD
	Range      bool      // request carried Range
	Authorized bool      // request carried Authorization
	Unsafe     bool      // unsafe or unknown method: invalidate on 2xx/3xx
	HasBody    bool      // the forwarded request carries a body (upload pool)
	Forwarded  Request   // keys.Request, see note
	Primary    store.Key // zero for ClassPass
	URITag     store.Tag
	OriginTag  store.Tag
	Origin     string    // scheme://host[:port]
	Partition  string    // origin + path, truncated to 512 bytes
	PartitionH uint64    // maphash of Partition, per-process seed
	ReqCC      httpcc.RequestDirectives // no-store, only-if-cached; the rest only with HonorRevalidation (FR-SRV-8)
	ClientCond ClientConditionals
	rangeHdr, ifRange []string // client's Range and If-Range lines, only for AsRangePass (FR-SRV-5)
}

func (c *Classified) AsRangePass() *Classified // ClassPass copy whose forwarded header (cloned) adds Range and If-Range; method stays GET (FR-FWD-4)

type ClientConditionals struct {
	IfNoneMatch     []string  // entity-tags as sent, or just "*"; nil when absent, any element is malformed, or the lines exceed MaxKeyedHeaderBytes (bound, P5)
	IfModifiedSince time.Time // zero when absent, repeated or not an HTTP-date
}
```

A nil `Request.Header` is treated as empty. `internal/keys` cannot import `weir` (import cycle). It defines its own `Request` struct with the same fields, and the root package converts with a field copy. The copy is cheap (strings and a header map reference).

Classification order:

1. Detect `CONNECT` (any form) and `Connection: upgrade` with an `Upgrade` field first and return `keys.ErrUpgrade`, which the root maps to `ErrUpgradeNotSupported` (FR-UPG-1, T-44); a CONNECT path is authority-form and would otherwise fail as `path`. Then validate per FR-VAL-1. Any failure returns `RequestError{Reason}` with reasons from a fixed list (`scheme`, `host`, `path`, `path-escape`, `query`, `query-params`, `method`).
2. Method: `GET`, `HEAD` are cacheable. `OPTIONS`, `TRACE` are safe but not cacheable: `ClassPass`, `FwdMethod`, no invalidation. Everything else (including lowercase `get`) is unsafe or unknown: `ClassPass`, `FwdMethod`, invalidation on 2xx/3xx.
3. Bypass: any `Bypass.Headers` present, or any `Bypass.Cookies` present in any `Cookie` line: `ClassPass`, `FwdBypass`.
4. Otherwise build the forwarded request and keys (§3.2 to §3.5).

For `ClassPass`, the forwarded request is the client request with hop-by-hop fields and any `Host` field removed (the host is `Forwarded.Host`), path and query untouched, body attached. No key is built except the URI tag (needed for invalidation). The URI tag is computed from the path and query after applying the same rewrite rules as cacheable requests (§3.4, and path normalization when enabled), even though the forwarded query stays untouched; otherwise `POST /p?utm_source=x` would invalidate a URI that no cached `GET` is stored under.

### 3.2 Canonical primary-key encoding

```
buf = "weir/key/v1"                 // domain separation and version
field(0x01, methodClass)            // "GET"
field(0x02, scheme)                 // lowercase
field(0x03, host)                   // normalized
field(0x04, path)                   // as forwarded
field(0x05, query)                  // as forwarded, may be empty
for each name in Key.Headers (config order):
    field(0x10, name); presence(0 or 1); if 1: field(0x11, normalizedValue)
for each name in Key.Cookies (config order):
    field(0x20, name); presence(0 or 1); if 1: field(0x21, value)
for each cookie left unmatched (a caller bug; see note):
    field(0x20, name); 0x01; field(0x21, value)
Primary = sha256(buf)

field(tag, b) = tag byte, uvarint(len(b)), b
presence(p)   = 0x00 or 0x01
```

Because every variable-length field is length-prefixed and every field is tagged, the encoding is injective. Cookie values arrive in `Key.Cookies` order, so the encoder pairs them with names in one pass. A value it cannot pair is still keyed after the loop: the same values build the forwarded `Cookie` header, so a caller bug splits the cache instead of forwarding an unkeyed cookie (INV-1). A property test ([07-testing-strategy.md §4](07-testing-strategy.md)) generates random field tuples and asserts that distinct tuples produce distinct buffers.

Implementation uses a pooled `[]byte` builder (`sync.Pool` of `*[]byte`, reset length to 0) and `sha256.Sum256`. No `fmt`, no string concatenation.

### 3.3 Variant key

```
buf = "weir/variant/v1" + Primary[:]
for name in VaryNames (lowercased, sorted, deduplicated at spec creation):
    field(0x30, name); presence; if present: field(0x31, normalizedValue(name, fwd.Header))
Variant = sha256(buf)
```

`normalizedValue` uses the registered normalizer for `Accept-Encoding` (its value in the forwarded request is already the bucket token, so this is identity) and the generic list normalizer otherwise (FR-KEY-11).

### 3.4 Query rewrite

```
func rewriteQuery(raw string, cfg) string:
    if raw == "" and no rules: return ""
    segs := split(raw, '&')                // no decoding
    out := segs[:0]
    for s in segs:
        if s == "": continue
        name := s[:indexOrLen(s, '=')]
        if len(cfg.keep) > 0 && !cfg.keep.match(name): continue
        if cfg.drop.match(name): continue
        out = append(out, s)
    if cfg.sort: slices.SortStableFunc(out, bytes-compare)
    return join(out, '&')
```

Matching compares raw bytes. `utm_source` and `utm%5Fsource` are different names; the second is kept (and therefore keyed and forwarded), which is safe because the origin receives exactly what was keyed. The validator already rejected malformed percent escapes and control bytes, so the raw segments are safe to forward.

### 3.5 Forwarded headers (strict mode)

```
out := http.Header{}
for name in Key.Headers: if v, ok := normalized(name); ok { out[name] = []string{v} }
copy if present: Authorization, Cache-Control, Pragma, traceparent, tracestate, X-Request-Id
for name in Forward.Allow: copy all lines if present
delete hop-by-hop fields, fields named in Connection, Host, conditionals, Range, Content-Length, Expect and Trailer (an Allow entry cannot bring them back)
if v := cookieHeader(keyedCookies()); v != "": out["Cookie"] = []string{v}
filter trace fields (FR-FWD-6)
out["Accept-Encoding"] = []string{aeBucket}  // always, set last, e.g. "gzip" or "identity"
```

`ForwardAll` copies all fields, then deletes hop-by-hop fields, the fields named in `Connection`, `Host`, `If-None-Match`, `If-Modified-Since`, `If-Match`, `If-Unmodified-Since`, `If-Range`, `Range`, and the body fields `Content-Length`, `Expect` and `Trailer` (the fetch has no body), filters trace fields, and sets `Accept-Encoding` to the bucket. The `Cookie` field stays as received.

Trace filtering drops `traceparent` and `tracestate` together unless there is exactly one `traceparent` line in version-00 form with lowercase hex and non-zero ids, and drops `X-Request-Id` unless it is one line of at most 128 visible ASCII bytes. With `NoTraceHeaders` all three go.

The client conditionals (`If-None-Match`, `If-Modified-Since`) are parsed into `ClientConditionals` before being dropped, so the engine can answer 304 itself.

### 3.6 Accept-Encoding normalizer

```
func aeBucket(lines []string, supported []string) string:
    if len(lines) == 0: return "identity"
    // RFC 9110 §12.5.3 rule 1 would allow any coding here; FR-KEY normalizer picks identity
    // because clients that omit the header often cannot decode compressed bodies.
    q := map[string]float64{}; star := -1.0
    for each member in splitList(lines joined by ","):   // splitList trims OWS, drops empties
        coding, params := cutParams(member)
        coding = asciiLower(coding)
        if !isToken(coding): continue                     // malformed member skipped
        w, ok := parseQ(params)                           // default 1; qvalue grammar per RFC 9110 §12.4.2
        if !ok: continue
        if coding == "*": star = w; continue
        if _, seen := q[coding]; seen: continue           // first occurrence wins
        q[coding] = w
    best, bestQ := "identity", 0.0
    for c in supported:
        w, ok := q[c]; if !ok && star >= 0 { w, ok = star, true }
        if ok && w > bestQ { best, bestQ = c, w }
    return best
```

Total input size above `MaxKeyedHeaderBytes` returns `"identity"` without parsing. An empty header value (`Accept-Encoding:`) means no coding wanted, and the loop returns `identity`. The function never allocates more than one small map; a later optimization may replace it with a fixed array since `supported` is at most a handful of codings.

### 3.7 Host normalization and validation

Lowercase ASCII; reject any byte other than `a`–`z`, `0`–`9`, `.`, `-`, `:`, `[` and `]` after lowercasing (percent-encoded reg-names are rejected); split port; validate IPv6 literal with `net/netip.ParseAddr`; strip port equal to the scheme default; strip one trailing dot. Maximum 255 bytes before normalization. The port is 1–65535 in at most five decimal digits; an empty port is rejected and leading zeros are dropped (`:0443` becomes `:443`, then stripped for `https`). A bracketed literal must parse as IPv6 (`[1.2.3.4]` is rejected) and is rewritten to `netip.Addr.String()` form (`[0:0::1]` becomes `[::1]`). A name that still ends in a dot after the one strip (`a..`) is rejected, so normalization is idempotent and every accepted host has one spelling. `Validate` returns the normalized host; key and forwarded request both use it (P2).

## 4. `internal/httpcc`

### 4.1 Parsing

```go
type ResponseDirectives struct {
	MaxAge, SMaxAge, SWR, SIE   Seconds // Seconds{V int64; Set, Invalid bool}
	NoStore, NoCache, Private   bool
	Public, MustRevalidate      bool
	ProxyRevalidate, MustUnderstand bool
	Duplicates                  bool // a delta-seconds directive (max-age, s-maxage, SWR, SIE) repeated with different values (FR-FRS-2)
}

func ParseResponse(h http.Header) ResponseDirectives
func ParseRequest(h http.Header) RequestDirectives // no-store, no-cache, max-age, min-fresh, max-stale, only-if-cached, plus Pragma: no-cache
```

Parser rules: split on commas outside quoted strings (a quote opens only as the first byte of an argument; an unclosed quote rescans the rest of the line so a later `private` or `no-store` still counts); directive names compared case-insensitively; arguments accept token or quoted-string; unknown directives ignored (RFC 9111 §5.2.3); `delta-seconds` parse rejects signs and non-digits, clamps above 2147483648.

### 4.2 Lifetime, age, permissions

```go
// Config carries the defaulted FreshnessConfig fields httpcc reads; httpcc cannot import the root package.
type Config struct {
	HeuristicFraction      float64
	HeuristicMax           time.Duration
	DefaultTTL             time.Duration
	DefaultSWR, DefaultSIE time.Duration
}

func Lifetime(d ResponseDirectives, h http.Header, status int, respTime time.Time, cfg Config) (lt time.Duration, heuristic bool)
func CorrectedInitialAge(ageHdr string, reqTime, respTime time.Time) time.Duration // age_value + response_delay (FR-FRS-4); Date is not used
func CurrentAge(e *store.Entry, now time.Time) time.Duration
func Jitter(lt time.Duration, frac float64, min time.Duration, u float64) time.Duration
func StaleWindows(d ResponseDirectives, cfg Config) (swr, sie time.Duration)
```

`StaleWindows` implements FR-STL-1 to FR-STL-3: returns (0, 0) when `MustRevalidate`, `ProxyRevalidate` or `NoCache`; origin values when set (even with `s-maxage`); otherwise each operator default applies per directive, when the origin did not send that directive and `s-maxage` is absent (an origin `stale-if-error` alone still gets `DefaultStaleWhileRevalidate`, and the reverse).

`Lifetime` returns 0 when any of the four delta-seconds directives is invalid or duplicated (FR-FRS-2), and when `Expires` is invalid or repeated. Lifetimes and stale windows, operator defaults included, are clamped to [0, 2147483648 s]. HTTP-dates in a zone other than GMT (for example `PST` or `GMT-8` in the RFC 850 form) are invalid, because `http.ParseTime` resolves zone abbreviations against the host's TZ.

`Jitter` returns `lt` unchanged when `lt < min` or `frac == 0`, else `lt - time.Duration(float64(lt) * frac * u)`. Pure, so the distribution test drives it with a deterministic `u` sequence.

Retention (`Entry.Expires`): `StoredAt + (Lifetime - CorrectedInitialAge) + max(SWR, SIE) + (Keep if the entry has an ETag or Last-Modified, else 0)`, and at least `StoredAt + 1s` so a zero-lifetime entry with a validator can still be revalidated once. For hit-for-miss markers: `StoredAt + HitForMissTTL`. For negatives: `StoredAt + Negative.TTL`. For vary specs: the maximum `Expires` among its variants.

### 4.3 Entry evaluation

```go
type State uint8 // Fresh, StaleSWR, NeedsValidation, Unusable

func Evaluate(e *store.Entry, ep store.Epoch, epOK bool, now time.Time) (st State, staleness time.Duration, sieOK bool)
```

```
age := CurrentAge(e, now)
expiry := lifetime_eff                      // relative to generation
if epOK:
    switch ep.Mode:
    case EpochHard:    return Unusable
    case EpochInvalid: return NeedsValidation, sieOK=false
    case EpochSoft:    // stale as of the purge time, windows capped at original expiry
        purgeAge := age - now.Sub(ep.At)     // the entry's age at purge time
        expiry = min(expiry, purgeAge)
    default:           return Unusable      // zero or unknown mode fails closed (T-9)
staleness := age - expiry
if staleness < 0 && !e.Flags.Has(FlagNoCache): return Fresh
if e.Flags.Has(FlagNoCache): return NeedsValidation, sieOK=false
swrOK := staleness <= e.SWR (and e.SWR > 0)
sieOK  = staleness <= e.SIE (and e.SIE > 0)
if swrOK: return StaleSWR
return NeedsValidation
```

`SWR` and `SIE` are already zero for entries that forbid stale serving, so `must-revalidate` and friends need no special case here. The `must-revalidate` 504 rule is applied by the engine on fetch failure (§6.6).

## 5. `store/memory`

### 5.1 Engine-facing rules

Algorithm, sizing and invariants are specified in [05-storage-interface-spec.md §5](05-storage-interface-spec.md). The LLD rules that matter to the engine:

- `Get` returns the stored pointer. Callers never mutate it (P4). The conformance suite checks that the memory store never mutates an entry after `Set` either.
- `Info().Remote` is false, so the engine calls it with the request context directly and does not allocate a timeout context on the hit path.

### 5.2 Store guard (engine side)

```go
type storeGuard struct {
	s       store.Store
	remote  bool
	timeout time.Duration
	mu      sync.Mutex
	fails   int
	openTil time.Time
	backoff time.Duration // 1s doubling to 30s
}
```

`get`, `set`, `newestEpoch`, `setEpoch`: if the guard is open, return `ErrUnavailable` immediately. If `remote`, wrap the context with `Timeouts.Store`. Map `context.DeadlineExceeded` to `ErrUnavailable`. On `ErrUnavailable` increment `fails`; at 5 open for `backoff` and double it. On success reset `fails` and `backoff`. `ErrNotFound` is a success for guard purposes.

## 6. Engine internals (package `weir`)

### 6.1 Engine struct

```go
type Engine struct {
	cfg      Config        // defaults applied, immutable after New
	kcfg     keys.Config   // compiled key config
	sg       *storeGuard
	ownStore bool
	flights  *coalesce.Table
	lim      *limiter.Limiter
	cb       *breaker.Breaker // nil when disabled
	mr       *missrate.Tracker // nil when disabled
	obs      Observer
	log      *slog.Logger
	rnd      func() float64

	mu        sync.Mutex         // orders setting closed against wg.Add
	closed    atomic.Bool        // written under mu; read without it on the Serve path
	closeDone chan struct{}      // closed when the first Close finishes
	bgCtx     context.Context    // canceled after Close's grace period
	bgCancel  context.CancelFunc
	wg        sync.WaitGroup     // every engine-owned goroutine
}
```

Engine goroutines start only through `goBackground(f)`, which checks `closed` and calls `wg.Go` while holding `mu`. `Close` sets `closed` under `mu`, so once it starts waiting no `wg.Add` can follow; without the lock, a `goBackground` racing `Close` could panic the WaitGroup (Add concurrent with Wait) or start a goroutine after `Close` returned. `mu` is never held across a wait (P8). A second `Close` waits on `closeDone` or its own context.

### 6.2 Serve

```
func (e *Engine) Serve(ctx, req, origin):
    if e.closed.Load(): return nil, ErrClosed
    c, err := keys.Classify(req, &e.kcfg)
    if err != nil: e.emit(EvKeyRejected, reason); return nil, err
    if c.Class == ClassPass: return e.pass(ctx, c, origin)
    return e.cacheable(ctx, c, origin, 0)

func (e *Engine) cacheable(ctx, c, origin, attempt):
    now := time.Now()
    lk := e.lookup(ctx, c, now)      // §6.3

    if lk.entry != nil:
        st, _, sieOK := httpcc.Evaluate(lk.entry, lk.epoch, lk.epochOK, now)
        switch st:
        case Fresh:
            e.maybeEarlyRefresh(c, lk, origin, now)
            return e.fromEntry(c, lk.entry, now, hitInfo(lk.entry, now))
        case StaleSWR:
            e.backgroundRefresh(c, lk, origin)
            return e.fromEntry(c, lk.entry, now, staleInfo(StaleWhileRevalidate))
        }
        if st == Unusable: lk.entry = nil            // hard-purged: behave exactly like a miss
        // NeedsValidation falls through with lk.entry kept for conditional headers and SIE
    if c.ReqCC.OnlyIfCached: return nil, ErrOnlyIfCached
    if lk.negative != nil && !(lk.entry != nil && sieOK): return e.fromNegative(lk.negative)
    if c.Range: return e.pass(ctx, c.AsRangePass(), origin)   // FR-SRV-5: even with a stale entry (T-37); adds the client's Range and If-Range lines
    if lk.marker || c.Authorized && lk.entry == nil || attempt > 0 && lk.ck == prevCK:
        return e.fetchDirect(ctx, c, lk, origin)          // §6.5, no coalescing
    return e.fetchCoalesced(ctx, c, lk, origin, attempt)  // §6.4; prevCK is the previous attempt's coalescing key
```

With `Client.HonorRevalidation`, a request `no-cache`, `max-age=0` or `Pragma: no-cache` turns a `Fresh` or `StaleSWR` result into `NeedsValidation` before the switch.

### 6.3 Lookup

```
func (e *Engine) lookup(ctx, c, now) lookupResult:
    rec, err := e.sg.get(ctx, c.Primary)
    lk := lookupResult{ck: c.Primary, fwd: FwdURIMiss}
    if err != nil: return lk                          // ErrNotFound or ErrUnavailable: miss
    switch rec.Kind:
    case KindResponse:   lk.entry = rec
    case KindHitForMiss: lk.marker = true
    case KindNegative:   lk.negative = rec
    case KindVarySpec:
        vk := keys.VariantKey(c.Primary, rec.VaryNames, c.Forwarded.Header)
        lk.ck, lk.spec = vk, rec
        v, err := e.sg.get(ctx, vk)
        if err != nil: lk.fwd = FwdVaryMiss; return lk
        switch v.Kind { case KindResponse: lk.entry = v; case KindHitForMiss: lk.marker = true; case KindNegative: lk.negative = v }
    if lk.entry != nil:
        if lk.entry.Expires.Before(now): lk.entry = nil; return lk   // stores may return expired records lazily
        lk.epoch, lk.epochOK, _ = e.sg.newestEpoch(ctx, lk.entry.Tags, lk.entry.RequestTime)
        lk.hit = true
        lk.fwd = FwdStale                             // used only if we end up forwarding
    return lk
```

Epochs are compared against `RequestTime` (when the request that produced the entry was sent), not `StoredAt`. A fetch that started before a purge and finished after it would otherwise store pre-purge content that survives the purge. For a 304-freshened entry `RequestTime` is the time the validation request was sent, so a validation racing a purge also counts as purged, which errs toward purging.

Negative and marker records are also checked against `Expires`. Epoch lookup errors are treated as "no epoch": failing open here serves an entry that might be purged, which is preferable to turning a store outage into a miss storm; the event `EvStoreError{op: "epoch"}` makes it visible. This choice is recorded in [06-threat-model.md](06-threat-model.md) T-9.

### 6.4 Coalesced fetch

```
func (e *Engine) fetchCoalesced(ctx, c, lk, origin, attempt):
    f, created := e.flights.Join(lk.ck, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
    if created:
        spec := e.fetchSpec(c, lk, classForeground)
        e.wg.Add(1); go func() { defer e.wg.Done(); e.runFlight(f, spec, origin) }()
    else:
        e.emit(EvCoalesceJoin)

    timer := time.NewTimer(e.cfg.Coalesce.FollowerMaxWait); defer timer.Stop()
    select:
    case <-f.Done():
        res := f.Result()
        if created && res.stream != nil:
            if f.ClaimStream(): return e.fromStream(c, res)   // oversized body, creator only
        if res.bgDropped: return e.cacheable(ctx, c, origin, attempt+1)   // joined a background flight that got no slot: fetch as foreground
        if res.err != nil: return e.onFetchError(c, lk, res)  // §6.6
        if created && res.entry != nil:
            return e.fromFetched(c, res, collapsed: false)     // the creator's own request was forwarded; storable or not
        if res.shareable && keys.VaryMatches(res.entry, c.Forwarded.Header):
            return e.fromFetched(c, res, collapsed: true)
        return e.cacheable(ctx, c, origin, attempt+1)          // FR-COA-5
    case <-timer.C:
        if created: f.CreatorGone()
        if lk.entry != nil && staleIfErrorOK(lk): return e.fromEntry(c, lk.entry, staleInfo(StaleCoalesceTimeout))
        return e.fetchDirect(ctx, c, lk, origin)
    case <-ctx.Done():
        if created: f.CreatorGone()
        return nil, ctx.Err()
```

`staleIfErrorOK(lk)` re-runs `Evaluate` at the current time and checks `sieOK`.

`runFlight`:

```
func (e *Engine) runFlight(f, spec, origin):
    // Values (for example a Caddy replacer) come from the creator's request
    // context; cancellation comes only from Close. No requester can cancel it.
    ctx, cancel := context.WithCancel(context.WithoutCancel(spec.reqCtx))
    stop := context.AfterFunc(e.bgCtx, cancel)
    defer stop(); defer cancel()
    res := e.fetch(ctx, spec, origin)   // §6.7; the fetch adds the origin timeout
    f.Publish(res)                          // closes Done, removes the table entry if still current
    if res.stream != nil && f.CreatorIsGone() && f.AbandonStream(): res.stream.Close()
```

Stream hand-off uses one atomic state on the flight (`unclaimed`, `claimed`, `abandoned`). `ClaimStream` and `AbandonStream` both CAS from `unclaimed`; exactly one wins, so the stream is closed exactly once and never leaked, whichever of the creator and the flight goroutine finishes first.

### 6.5 Direct fetch

`fetchDirect` runs `e.fetch` on the request goroutine with `ctx` (plus the origin timeout). Used for markers, authorized-request misses, the second attempt after a non-reusable flight result, and follower timeouts. The result is stored if storable, exactly like a flight's. It never creates a flight, so it never blocks anyone else.

### 6.6 Fetch failure handling

```
func (e *Engine) onFetchError(c, lk, res):
    if lk.entry != nil:
        st, _, sieOK := Evaluate(lk.entry, lk.epoch, lk.epochOK, time.Now())
        if sieOK: return e.fromEntry(c, lk.entry, staleInfo(res.staleReason()))
        if lk.entry.Flags.Has(FlagMustRevalidate|FlagProxyRevalidate) && res.resp == nil:
            return nil, ErrMustRevalidate
    if res.originHealthFailure && lk.entry == nil && !c.Authorized && !e.cfg.Negative.Disable && !c.Range:   // FR-NEG-1, FR-NEG-4, T-31
        e.sg.set(ctx, lk.ck, negativeEntry(res))
    if res.resp != nil: return e.passResponse(c, res)   // origin's 5xx: fresh reader over res.respBody per waiter
    return nil, res.err                                 // ErrOrigin, ErrOriginTimeout, ErrShed, ErrCircuitOpen
```

`res.staleReason()` maps: shed to `StaleShed`, breaker open to `StaleCircuitOpen`, everything else to `StaleIfError`.

A 500 response goes through the same function: it is an error condition for stale serving (RFC 5861) but not an origin-health failure, so it never writes a negative entry.

### 6.7 The fetch function (P3)

This is the only caller of `Origin.Fetch`.

```go
type fetchSpec struct {
	class     limiter.Class // Foreground, Background, Warm
	partition uint64
	fwd       keys.Request  // forwarded request (GET for HEAD clients)
	prior     *store.Entry  // for conditional headers and 304 merge; may be nil
	c         *keys.Classified
	ck        store.Key
	spec      *store.Entry  // current vary spec, may be nil
	streaming bool          // pass-through: do not buffer, release slot at headers
	reqCtx    context.Context // creator's request context, used for values only in flights
}

type fetchResult struct {
	resp      *Response     // pass-through (streamed, single consumer) or 5xx head to pass through
	respBody  []byte        // buffered body of a 5xx; each waiter gets its own reader
	entry     *store.Entry  // built entry when storable or shareable
	shareable bool
	stored    bool
	stream    io.ReadCloser // oversized body continuation (creator only)
	err       error
	bgDropped bool          // background class found no slot; foreground waiters must retry, not fail
	originHealthFailure bool
	fwdStatus int
}
```

```
func (e *Engine) fetch(ctx, s, origin) fetchResult:
    probe, err := e.cb.Allow()              // ErrCircuitOpen when open
    if err: return errResult(ErrCircuitOpen)
    permit, err := e.lim.Acquire(ctx, s.class, s.partition)
    if err: e.cb.Cancel(probe); return errResult(ErrShed, bgDropped: s.class == Background)
    released := false
    release := func() { if !released { released = true; permit.Release() } }
    defer release()

    tctx, cancel := context.WithTimeoutCause(ctx, timeoutFor(s.class), ErrOriginTimeout)   // ctx: request ctx for direct/pass, detached ctx for flights; the cause tells the origin timeout from the caller's deadline
    defer cancel()                           // not deferred for streaming; see below
    req := toWeirRequest(s.fwd); addConditionals(req, s.prior)
    t0 := time.Now()
    resp, err := safeFetch(origin, tctx, req)      // recovers panics into *OriginError; (nil, nil) and statuses outside 200..999 (1xx, and what net/http would reject) become *OriginError; nil Header becomes empty, nil Body becomes http.NoBody
    outcome := classify(resp, err, tctx)            // success | gateway failure | other
    e.cb.Record(probe, outcome)

    if err != nil: return errResult(timeoutOrOrigin(err, ctx, tctx), originHealth: true)   // §1.3
    if s.streaming:
        release()                            // slot released at headers (FR-LIM-1)
        resp.Body = cancelOnClose(resp.Body, cancel)  // origin timeout still bounds the stream
        return fetchResult{resp: resp}
    if resp.StatusCode == 304 && s.prior != nil:
        drain(resp.Body)
        if strongETagMismatch(resp, s.prior):         // RFC 9111 §4.3.4: must not update
            retry once without conditionals under the same permit and continue below with that response
        ent := freshen(s.prior, resp, t0, time.Now(), e.rnd)
        e.store(s, ent)
        return fetchResult{entry: ent, shareable: true, stored: ..., fwdStatus: 304}
    body, over, rerr := readUpTo(resp.Body, e.cfg.Storable.MaxObjectBytes)
    if rerr != nil: return errResult(ErrOrigin, originHealth: true)   // truncated body is never stored
    if over:
        return fetchResult{resp: resp, stream: multiReadCloser(body, resp.Body, cancel)} // cancel deferred to stream close
    if isGatewayFailure(resp.StatusCode) || resp.StatusCode == 500:
        return fetchResult{resp: headOnly(resp), respBody: body, err: statusErr, originHealthFailure: gateway}
    decision := storability(s, resp, body)    // FR-STO-1..10
    ent := buildEntry(s, resp, body, t0, time.Now(), decision, e.rnd)
    if decision.ok: e.store(s, ent)
    else if s.prior == nil && decision.responseDriven && !s.c.Authorized && !s.c.ReqCC.NoStore:
        e.setMarker(s.ck)                    // FR-STO-12, T-31: only for response-driven reasons under keyed inputs
    return fetchResult{entry: ent, shareable: decision.ok, stored: ...}
```

`cancelOnClose` and the oversized path keep the timeout context alive until the consumer closes the body; `defer cancel()` is skipped on those paths (the implementation uses a flag, not two code paths with different defers).

`e.store(s, ent)` first reads the record currently at the target key; if it is a response whose `Date` (then `ResponseTime`) is later than the new entry's, the write is skipped, so a slow, aged flight finishing late never replaces a newer response (RFC 9111 §4: the most recent response wins). The record the request itself found at lookup is exempt: it was already judged stale or unusable, and an origin whose clock once ran ahead would otherwise pin a purged or invalidated entry until it expires. The read-then-write is not atomic; the race window can only let an older response win when two writes land within the same store round trip, and the next refresh corrects it. It then writes the variant entry first, then the vary spec (or the entry under the primary key when there is no `Vary`), so a concurrent reader that finds the spec usually finds the variant. When the response's `Vary` differs from the stored spec, the new spec replaces it and older variants age out.

`setMarker` and negative writes use the same read-before-write: they skip the write when the current record at the key is a response, so a marker or negative entry never replaces a response that a concurrent fetch just stored.

`fromStream` for a `HEAD` client closes the stream immediately and returns headers only; the origin transfer is canceled rather than downloaded for nothing.

Adapters must not write into header value slices in place (`h[k][0] = v`); `Set`, `Add` and `Del` are safe (§6.10).

When the vary spec already lists `MaxVariants` live variants and the new variant is not among them, the entry is not stored (`EvVaryOverflow`). Spec updates are read-modify-write without compare-and-swap, so concurrent writers of different variants can exceed `MaxVariants`; the overshoot is bounded by `MaxPerPartition`, because all writers for one URI share a partition. Phase 2.5 may add CAS via the Valkey store.

### 6.8 Background refresh and early refresh

```
func (e *Engine) backgroundRefresh(c, lk, origin):
    if e.closed.Load(): return
    f, created := e.flights.Join(lk.ck, time.Now(), e.cfg.Coalesce.LeaderMaxAge)
    if !created: return                       // someone is already fetching this key
    f.CreatorGone()                           // no requester will claim an oversized stream; runFlight closes it
    spec := e.fetchSpec(c, lk, classBackground)
    e.wg.Add(1)
    go func() { defer e.wg.Done(); e.runFlight(f, spec, origin) }()
```

The limiter's `Background` class uses `TryAcquire`. If it fails, `fetch` returns `ErrShed` with `bgDropped` set, the flight publishes that result, and nothing else happens (`EvRefreshDropped`). Foreground requests that joined this flight in the meantime see `bgDropped` and re-enter lookup as a direct foreground fetch (they may queue), rather than being shed by a rule meant only for background work.

`maybeEarlyRefresh` draws `u := 1 - e.rnd()` (so `u` is in (0, 1]) and calls `backgroundRefresh` when `-float64(Δ) * beta * math.Log(u) >= float64(remaining)`.

### 6.9 Pass-through

```
func (e *Engine) pass(ctx, c, origin):
    res := e.fetch(ctx, fetchSpec{class: Foreground, partition: c.PartitionH, fwd: c.Forwarded, streaming: true}, origin)
    if res.err != nil: return nil, res.err
    if c.Unsafe && res.resp.StatusCode < 400: e.invalidate(ctx, c, res.resp)   // §7
    res.resp.Cache = CacheInfo{Fwd: c.FwdReason, FwdStatus: res.resp.StatusCode}
    return res.resp, nil
```

### 6.10 Building responses

`fromEntry` clones the stored header map shallowly (a new map whose value slices are shared). This is safe only because `buildEntry` and `freshen` store every header value slice clipped with `slices.Clip`, so `len == cap` and a caller's `Header.Add` (which appends) always reallocates instead of writing into the stored backing array. `Header.Set` and `Del` replace or remove the map entry and never touch the shared slice. A test (`TestServedHeaderMutationDoesNotLeak`) mutates a served response's headers every way `http.Header` allows and asserts the next hit is unchanged. After cloning, `fromEntry` sets `Age`, appends `Cache-Status`, evaluates client conditionals (FR-SRV-2) and returns either a 304 with no body or the status with `io.NopCloser(bytes.NewReader(entry.Body))`. For `HEAD` the body is `http.NoBody`. Header cloning is the main allocation on the hit path; it is required because adapters may add headers to the response.

## 7. Purge and invalidation

```
func (e *Engine) Purge(ctx, p):
    tags := []
    if p.All: tags += TagGlobal
    for u in p.URLs: c := keys.ClassifyURL(u) (validation errors abort); tags += c.URITag
    if len(p.Groups) > 0:
        if p.Origin == "": return RequestError{"purge-origin"}
        o := keys.NormalizeOrigin(p.Origin)
        for g in p.Groups: tags += TagGroup(o, g)
    mode := EpochSoft; if p.Mode == PurgeHard: mode = EpochHard
    at := time.Now()
    for t in tags: if err := e.sg.setEpoch(ctx, t, Epoch{at, mode}); err != nil { return err }
    e.emit(EvPurge{...})

func (e *Engine) invalidate(ctx, c, resp):
    tags := [c.URITag]
    for h in [Location, Content-Location]:
        if u, ok := resolveSameOrigin(c, resp.Header.Get(h)); ok: tags += TagURI(u)   // u validated and query-rewritten like a request; invalid values are ignored
    write EpochInvalid for each tag so far                // RFC 9111 §4.4
    if !e.cfg.CacheGroups.Ignore:
        for g in sfv.ParseStringList(resp.Header["Cache-Group-Invalidation"]) (bounded by Limits):
            write EpochSoft for TagGroup(c.Origin, g)     // FR-INV-2, T-28
```

`NewestEpoch` returns the most severe mode among epochs newer than the entry, so a soft purge after a hard purge never resurrects the hard-purged entry.

## 8. Other internal packages

### 8.1 `internal/coalesce`

```go
type Table struct {
	shards [64]shard // index: first byte of key & 63 (keys are SHA-256 output)
}
type shard struct {
	mu sync.Mutex
	m  map[store.Key]*Flight
}
type Flight struct {
	done    chan struct{}
	res     any        // set before done is closed; read only after <-done
	started time.Time  // monotonic
	stream  atomic.Uint32
	creatorGone atomic.Bool
}

func (t *Table) Join(k store.Key, now time.Time, maxAge time.Duration) (f *Flight, created bool)
func (f *Flight) Publish(res any)     // closes done exactly once and removes itself from the table if still current
```

`Join` under the shard lock: if an entry exists and `now.Sub(started) < maxAge`, return it; otherwise create a new flight, replace the map entry, return `created = true`. The aged flight keeps a back-pointer to its shard so `Publish` deletes the map entry only if `m[k] == f`.

Shard selection by key byte is safe here (unlike the store) because flights are short-lived and bounded by the limiter; an attacker grinding keys into one shard only contends one mutex among at most `MaxConcurrent + MaxQueue` flights.

### 8.2 `internal/limiter`

```go
type Class uint8 // Foreground, Background, Warm

type Limiter struct {
	mu         sync.Mutex
	max        int
	reserve    int
	perPart    int
	inflight   int
	byPart     map[uint64]int32        // only partitions with inflight > 0; len <= max
	throttled  map[uint64]int32        // cap overrides from missrate; bounded by TopK
	queue      list[*waiter]           // FIFO, len <= maxQueue
	maxQueue   int
	maxWait    time.Duration
}

type waiter struct {
	part  uint64
	class Class
	ready chan struct{} // closed when granted
	granted bool
}

func (l *Limiter) Acquire(ctx context.Context, c Class, part uint64) (*Permit, error)
func (p *Permit) Release()
```

Admission check `canRun(class, part)`: `inflight < max`, and for `Background` and `Warm` `inflight < max - reserve`, and `byPart[part] < capFor(part)`.

`Acquire`:

1. Lock. If `canRun` and no queued waiter is eligible ahead of us (FIFO fairness only among runnable waiters), take the slot, unlock, return.
2. `Background`: unlock, return `ErrShed` (never queues).
3. Queue full: unlock, return `ErrShed`.
4. Append a waiter, unlock. `select` on `ready`, `ctx.Done()`, and a timer of `maxWait` (`Warm` waits on `ctx` only).
5. On timeout or cancellation: lock; if `granted` became true in the race, keep the slot and return it; else remove the waiter; unlock; return `ErrShed` or `ctx.Err()`.

`Release`: lock; decrement `inflight` and `byPart[part]` (delete at zero); walk the queue from the head and grant every waiter for which `canRun` holds, marking `granted`, updating counters and closing `ready`; unlock. Waiters whose partition is at its cap are skipped, not dropped, so one saturated partition does not block others (FR-LIM-3). The walk is O(queue length) in the worst case; with `MaxQueue = 1024` this is acceptable, and the benchmark in M4 checks it.

Waiting uses a channel and a timer, both durably blocking inside a synctest bubble (P8).

### 8.3 `internal/breaker`

All methods are nil-receiver safe (a nil `*Breaker` allows everything and records nothing), so `Breaker.Disable` needs no branches at call sites. The same holds for `*missrate.Tracker`.


```go
type Breaker struct {
	mu        sync.Mutex
	state     State // Closed, Open, HalfOpen
	buckets   [10]bucket // {start time.Time; ok, fail uint32}
	openUntil time.Time
	openFor   time.Duration // current, doubles on reopen
	probes    int           // in flight while HalfOpen
	cfg       Config
	rnd       func() float64
	onChange  func(from, to State)
}

type Probe struct{ isProbe bool }
func (b *Breaker) Allow() (Probe, error)
func (b *Breaker) Record(p Probe, o Outcome)
func (b *Breaker) Cancel(p Probe)
```

Buckets rotate lazily: on each call, buckets whose `start` is older than `Window` are zeroed. `Allow` in `Open` returns `ErrCircuitOpen` until `openUntil`, then moves to `HalfOpen`. In `HalfOpen` it grants at most `HalfOpenProbes` probes and rejects everything else. `Record` with a probe: success closes (reset buckets, reset `openFor`), failure reopens with `openFor = min(openFor*2, MaxOpenFor)` and `openUntil = now + openFor*(0.8 + 0.4*rnd())`. `Record` in `Closed` updates the bucket and trips when volume and ratio thresholds hold.

### 8.4 `internal/missrate`

```go
type Tracker struct {
	mu       sync.Mutex
	window   time.Duration
	start    time.Time
	counters []counter          // len <= TopK
	index    map[uint64]int     // partition hash -> counters index
	emit     func(Anomaly)
}
type counter struct {
	h           uint64
	sample      string // partition string, truncated to 256 bytes, set when the counter is (re)assigned
	reqs, misses uint64
	err         uint64 // Space-Saving overestimation bound inherited on replacement
}
```

`Observe(h, sample, miss)`: rotate if the window ended (evaluate anomalies, emit, reset); if `h` is tracked, increment; else if not full, add; else replace the counter with the minimum `reqs`, setting `reqs = min+1`, `err = min`, `misses = 1 if miss`. `misses` counts only since the counter was (re)assigned, so it is exact for that span and a lower bound overall. Anomaly check at rotation: `misses >= MinMisses` and `misses / max(1, reqs - err) >= MinRatio` (capped at 1), so replaced counters do not produce false alarms. Throttle updates go to the limiter through a callback that replaces the limiter's whole `throttled` map once per window (at most `TopK` entries), so throttles expire by themselves. Partition strings are safe to log: validation already rejected control bytes (FR-VAL-1).

The engine calls `Observe` once per cacheable request after the outcome is known (hit or miss), not in `lookup`; the pseudo-code in §6.2 shows the call site simplified.

### 8.5 `internal/sfv`

`ParseStringList(lines []string, maxMembers, maxLen int) ([]string, error)`: RFC 9651 §4.2.1 list parsing restricted to members that are Strings (members with parameters: parameters ignored; members of other types: error). Any error means "not a valid field": for `Cache-Groups` the response is not stored (FR-STO-10); for `Cache-Group-Invalidation` nothing is invalidated beyond the target URI and a warning is logged.

## 9. Observability

### 9.1 Observer

```go
type Observer interface{ Observe(Event) }

type Event struct {
	Kind      EventKind
	Time      time.Time
	Partition string        // truncated to 256 bytes; empty for engine-wide events
	Duration  time.Duration // fetch, wait, or open duration where relevant
	Status    int
	Reason    string        // fixed vocabulary per kind, safe as a metric label
	Info      CacheInfo     // for EvRequest
}
```

### 9.2 Event catalog

| Kind | Emitted when | Reason vocabulary |
|---|---|---|
| `EvRequest` | every `Serve` return | `hit`, `stale`, `miss`, `revalidated`, `pass`, `bypass`, `negative`, `error` |
| `EvFetchStart`, `EvFetchEnd` | around every `Origin.Fetch` | `foreground`, `background`, `warm`, `pass` |
| `EvCoalesceJoin` | a request joined an existing flight | |
| `EvCoalesceTimeout` | follower wait expired | `stale`, `direct` |
| `EvShed` | limiter refused | `queue-full`, `queue-timeout`, `background` |
| `EvStaleServed` | a stale response was served | `swr`, `sie`, `shed`, `circuit-open`, `coalesce-timeout` |
| `EvRefreshDropped` | background refresh not started | `no-slot`, `circuit-open`, `closed` |
| `EvBreakerState` | breaker transition | `closed`, `open`, `half-open` (new state) |
| `EvStoreError` | guard saw `ErrUnavailable` | `get`, `set`, `epoch`, `set-epoch` |
| `EvStoreBreaker` | store guard opened or closed | `open`, `closed` |
| `EvKeyRejected` | validation failed | `RequestError.Reason` values |
| `EvNotStored` | storability failed | `method` (defensive: cacheable forwards are always GET), `status`, `no-store`, `private`, `authorization`, `set-cookie`, `vary-star`, `vary-sensitive`, `vary-strict`, `vary-too-many`, `no-freshness`, `too-large`, `incomplete`, `groups`, and `vary-unsupported` in M1 to M6 only (removed by M7) |
| `EvVaryOverflow` | variant cap reached | |
| `EvNegativeServed` | negative entry used | |
| `EvPurge` | `Purge` or invalidation wrote epochs | `soft`, `hard`, `invalid` |
| `EvMissRateAnomaly` | window closed with an anomalous partition | `flag`, `throttle` |
| `EvEvict` | memory store evicted (batched per shard per call) | `small`, `main`, `expired` |
| `EvMode` | `SetMode` changed the incident mode or it expired (FR-MODE-1) | `normal`, `stale-on-error`, `bypass` (new mode) |

Reasons never contain request data. `Partition` is the only field derived from request input; exporters must not use it as a metric label.

### 9.3 Prometheus mapping (module `observe/prom`, M10)

| Metric | Type | Labels |
|---|---|---|
| `weir_requests_total` | counter | `outcome` |
| `weir_origin_fetches_total` | counter | `class`, `result` (`ok`, `gateway_failure`, `error`) |
| `weir_origin_fetch_seconds` | histogram | `class` |
| `weir_origin_inflight` | gauge | |
| `weir_limiter_queue_depth` | gauge | |
| `weir_shed_total` | counter | `reason` |
| `weir_stale_served_total` | counter | `reason` |
| `weir_coalesced_total` | counter | |
| `weir_coalesce_timeouts_total` | counter | `fallback` |
| `weir_breaker_state` | gauge (0 closed, 1 half-open, 2 open) | |
| `weir_breaker_transitions_total` | counter | `to` |
| `weir_store_errors_total` | counter | `op` |
| `weir_not_stored_total` | counter | `reason` |
| `weir_key_rejections_total` | counter | `reason` |
| `weir_negative_served_total` | counter | |
| `weir_purges_total` | counter | `mode` |
| `weir_miss_rate_anomalies_total` | counter | `action` |
| `weir_evictions_total` | counter | `queue` |
| `weir_store_bytes` | gauge | |

Gauges for in-flight and queue depth come from an optional `Stats()` method on the engine (`EngineStats{Inflight, Queued, BreakerState, StoreBytes}`), polled by the exporter's collector, rather than from events.

## 10. `weirhttp`

```go
// Middleware serves requests through e, using origin for misses.
func Middleware(e *weir.Engine, origin weir.Origin) func(http.Handler) http.Handler

// Handler is Middleware without a next handler: every request goes through e.
func Handler(e *weir.Engine, origin weir.Origin) http.Handler

// TransportOrigin sends forwarded requests to Target with Transport.
type TransportOrigin struct {
	Target    *url.URL          // scheme and host of the origin
	Transport http.RoundTripper // nil: http.DefaultTransport
	Rewrite   func(*http.Request) // optional hook for Via, auth to origin, etc.
}

// HandlerOrigin calls an in-process http.Handler, buffering nothing: the
// handler writes into a pipe that becomes the response body.
type HandlerOrigin struct{ Handler http.Handler }

func RequestFrom(r *http.Request) *weir.Request
func WriteResponse(w http.ResponseWriter, resp *weir.Response) error
func WriteError(w http.ResponseWriter, err error)
```

`RequestFrom` takes `Path` and `RawQuery` from `r.RequestURI` (split at the first `?`), not from `r.URL`, so the engine sees the bytes the client sent. For absolute-form request targets it uses the path of the parsed URL's `EscapedPath()` and `RawQuery`. `Scheme` is `https` when `r.TLS != nil`, else `http`. `Host` is `r.Host`.

`TransportOrigin.Fetch` builds `*http.Request` with `URL.Opaque` set to the forwarded path so `net/url` does not re-encode it, sets `Host` to the forwarded host, copies headers, and calls `RoundTrip` with the given context.

`HandlerOrigin` runs the handler on a goroutine writing into an `io.Pipe`-backed `ResponseWriter` and returns once headers are written (or the handler returns). This is the shape the Caddy adapter reuses for `next`.

## 11. Allocation and performance notes

Hit path allocations to expect: forwarded header map (strict mode builds a small map), key buffer (pooled), response header clone, `Response` struct, body reader. Keep `Classify` free of `fmt`, `strings.Split` on untrusted input into large slices (use index loops), and regexes. `Key.Headers` and cookie names are pre-canonicalized at `New`.

The memory store's hit path takes only a shard read lock plus an atomic frequency update (05 §5.3). Nothing else runs under it.

## 12. Goroutine inventory

| Goroutine | Started by | Bounded by | Ends when |
|---|---|---|---|
| flight fetch | first requester of a key, background refresh | `MaxConcurrent + MaxQueue` flights | fetch done and published |
| warm workers | `Warm` | `Warm.Concurrency` | `Warm` returns |

All are added to `e.wg`. `Close` sets `closed`, waits on `wg` until its context ends, then cancels `bgCtx` (which cancels in-flight origin calls via the fetch context derived from it) and waits again.

## 13. Phase 1.x designs (M11 to M15)

### 13.1 Single range (M11)

`internal/httpcc.ParseRange(h string, size int64) (start, end int64, kind RangeKind)` with kinds `RangeNone` (absent, invalid, unknown unit, multi-range: serve 200), `RangeOK`, `RangeUnsatisfiable`. Grammar per RFC 9110 §14.1.2; digits parsed with overflow checks; header longer than 256 bytes is `RangeNone`. `If-Range` evaluation: strong `ETag` comparison, or an HTTP-date equal to the stored `Last-Modified` when that date is at least one second before the stored `Date` (RFC 9110 §8.8.2.2 strong-validator rule); anything else means the full 200. `fromEntry` slices `entry.Body[start:end+1]` into a `bytes.Reader`, sets `Content-Range` and `Content-Length`, status 206. Fuzz target `FuzzRange`.

FR-RNG-4's background fill: in `pass()` for a range miss, after headers arrive, if status is 206, `Content-Range` total is known and at most `MaxObjectBytes`, and `storability` passes with the status check skipped, call `backgroundRefresh` with a spec whose forwarded request has `Range` removed.

### 13.2 Targeted fields (M12)

`internal/sfv.ParseDictionary(lines []string, maxMembers int) (Dict, error)` covering RFC 9651 §4.2.2 (tokens, integers, decimals, strings, booleans, parameters ignored). `httpcc.ParseResponse` gains an input: the ordered target list. It returns directives from the first valid targeted field, then ORs in `private`, `no-store`, `no-cache` from `Cache-Control` (FR-TCC-3). Integer values that are decimals or negative make the field invalid (RFC 9213 §2.1 says not to coerce). `fromEntry` deletes `Weir-Cache-Control` from the cloned header map.

### 13.3 Snapshot (M13)

Writer runs inside `memory.Store.Close(ctx)`: `os.CreateTemp(dir, ".weir-snap-*")`, `Chmod(0600)`, buffered writer, per-shard read lock while copying node pointers (entries are immutable so encoding happens outside the lock), trailer, `Sync`, `Rename`. Loader in `memory.New`: open, verify magic and trailer first (seek to end), then stream records. `Close` takes a context; the engine's `Close` passes its own, so the adapter's shutdown grace period bounds snapshot time.

### 13.4 Per-host fairness (M14)

Limiter: `byHost map[uint64]int32` alongside `byPart`; `canRun` adds `byHost[host] < MaxPerHost` when enabled. The host hash is computed once in `Classify` from the normalized host. Memory store: see [05 §5.3](05-storage-interface-spec.md) quota paragraph.

### 13.5 Eager purge (M15)

`Purge` writes epochs exactly as before, then, when `Eager`, type-asserts `store.Scrubber` and calls `Scrub` with the same tags. The returned count goes into `EvPurge` as `Status` (number scrubbed).

## 14. Designs for decisions D25 to D42

- Upload pool (FR-LIM-7): the limiter holds two independent pools with the same algorithm (§8.2): `main` and `upload`. `Classify` sets `HasBody` for `ClassPass` requests when `Request.Body` is neither nil nor `http.NoBody` and the request did not declare `Content-Length: 0`. A cacheable request never has one: its body is not forwarded (T-5), so a fat GET cannot take an upload slot. Partition and host caps apply within each pool.
- Timeouts (FR-TMO-*): the fetch context carries `Timeouts.Origin` until the buffered body is read. For streams, `fetch` swaps the deadline context for a cancel-only context once headers arrive, and wraps the body in an idle-timeout reader: each `Read` arms a timer of `StreamIdle` (reset per successful read) whose expiry cancels the context. Timers are durably blocking in synctest, so the idle behavior is testable.
- Upgrades (FR-UPG-1): checked first in `Classify`, before validation of the path (so `CONNECT host:port` authority-form targets never hit the path validator).
- Event streams (FR-STR-1): checked in `fetch` right after headers, before `readUpTo`; such a response takes the streaming path with `shareable = false` and no marker.
- Trace headers (FR-FWD-6): `internal/keys` validates `traceparent` with a fixed-length byte check (55 bytes, lowercase hex, version `00`, non-zero ids) and copies the three headers into the forwarded request after the allowlist step.
- Stripped-cookie report (FR-OBS-5): a `missrate`-style Space-Saving summary of 32 string counters with a deadline; `Observe` becomes a no-op after the report is logged, so the steady-state hot path pays one atomic load.
- Modes (FR-MODE-*): `atomic.Pointer[modeState]{mode, until}` read once per `Serve`; expiry checked against `time.Now()`. `ModeBypass` routes to `pass()`; `ModeStaleOnError` widens `sieOK` in `onFetchError` with the 24 h cap and the forbidden-flag checks.
- Memory sizing (FR-MEM-1): computed in `New` when it builds the default store.
- Vary reclaim (D37): when updating a vary spec, refs with past `Expires` are dropped; refs whose `Get` returns `ErrNotFound` during the same update are dropped too (one extra read per ref, at most `MaxVariants`, only on spec writes).

