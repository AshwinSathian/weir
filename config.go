package weir

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// Config configures an Engine (01 §6, 04 §1.1). The zero value is valid and
// yields the defaults; every boolean is written so that false is the default.
// New copies it, so later changes to the caller's slices have no effect.
type Config struct {
	Store         store.Store // nil: memory store sized per FR-MEM-1
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
	CacheStatus   string // Cache-Status member name (FR-SRV-9); "": "Weir"
	NoCacheStatus bool   // omit the Cache-Status header (T-27)
	Observer      Observer
	Logger        *slog.Logger   // nil: discard
	Rand          func() float64 // [0, 1); safe for concurrent use; nil: rand.Float64
}

// KeyConfig controls which request parts form the cache key (FR-KEY).
type KeyConfig struct {
	QueryDrop      []string // exact names or "prefix*"
	QueryKeep      []string // allowlist; empty means keep all
	QuerySort      bool
	NormalizePath  bool
	Headers        []string // canonicalized with http.CanonicalHeaderKey
	Cookies        []string // order is the forwarded order
	Vary           VaryMode
	VaryAllow      []string // canonicalized; required for sensitive Vary names
	MaxVaryHeaders int      // 0: 8
	MaxVariants    int      // 0: 8
	AcceptEncoding []string // empty: ["gzip"]; lowercased
}

// VaryMode selects how response Vary headers are handled.
type VaryMode uint8

// VaryMode values.
const (
	VaryAuto VaryMode = iota
	VaryStrict
)

// ForwardConfig controls which request headers reach the origin (FR-FWD).
type ForwardConfig struct {
	Mode           ForwardMode
	Allow          []string // canonicalized; added to the trace headers
	NoTraceHeaders bool     // D29: do not forward traceparent, tracestate, X-Request-Id
}

// ForwardMode selects the forwarding policy.
type ForwardMode uint8

// ForwardMode values.
const (
	ForwardStrict ForwardMode = iota
	ForwardAll                // logs a warning at New
)

// BypassConfig lists request cookies and headers that skip the cache.
type BypassConfig struct {
	Cookies               []string
	Headers               []string      // canonicalized
	ReportStrippedCookies time.Duration // FR-OBS-5; 0: 5m; negative disables
}

// StorableConfig limits what may be stored (FR-STO).
type StorableConfig struct {
	Statuses       []int // nil: default set; never 206, 304, 500, 502, 503, 504
	MaxObjectBytes int64 // 0: 1 MiB, body plus headers
	StripSetCookie bool
	StreamTypes    []string // Content-Type media types streamed like text/event-stream (FR-STR-1)
}

// FreshnessConfig tunes lifetimes, jitter and early refresh (FR-FRS).
type FreshnessConfig struct {
	Jitter                      float64 // 0: 0.10 unless NoJitter; [0, 0.5]
	NoJitter                    bool
	JitterMinLifetime           time.Duration // 0: 10s
	EarlyRefreshBeta            float64       // 0: 1.0
	NoEarlyRefresh              bool
	HeuristicFraction           float64       // 0: 0.1
	HeuristicMax                time.Duration // 0: 1h
	DefaultTTL                  time.Duration // 0: none
	DefaultStaleWhileRevalidate time.Duration // 0: none
	DefaultStaleIfError         time.Duration // 0: none
	Keep                        time.Duration // 0: 5m; only entries with a validator
}

// CoalesceConfig bounds request coalescing (FR-COA).
type CoalesceConfig struct {
	LeaderMaxAge    time.Duration // 0: min(10s, Timeouts.Origin); at most Timeouts.Origin
	FollowerMaxWait time.Duration // 0: min(10s, Timeouts.Origin); at most Timeouts.Origin
	HitForMissTTL   time.Duration // 0: 30s
}

// LimiterConfig bounds origin concurrency (FR-LIM).
type LimiterConfig struct {
	MaxConcurrent     int           // 0: 64
	MaxQueue          int           // 0: 1024
	MaxQueueWait      time.Duration // 0: 2s
	MaxPerPartition   int           // 0: 16; clamped to MaxConcurrent
	ReserveForeground int           // 0: MaxConcurrent/4, at least 1 when MaxConcurrent >= 2
	MaxPerHost        int           // 0: off (M14)
	MaxUpload         int           // D25; 0: MaxConcurrent/4, at least 1
}

// BreakerConfig tunes the origin circuit breaker (FR-BRK).
type BreakerConfig struct {
	Window         time.Duration // 0: 10s (10 buckets)
	MinRequests    int           // 0: 20
	FailureRatio   float64       // 0: 0.5; (0, 1]
	OpenFor        time.Duration // 0: 5s
	MaxOpenFor     time.Duration // 0: max(60s, OpenFor); at least OpenFor
	HalfOpenProbes int           // 0: 1
	CountStatus500 bool
	Disable        bool
}

// NegativeConfig tunes negative caching of origin errors (FR-NEG).
type NegativeConfig struct {
	TTL     time.Duration // 0: 2s
	Disable bool
}

// MissRateConfig tunes the miss-rate detector (FR-MIS).
type MissRateConfig struct {
	Window    time.Duration // 0: 10s
	TopK      int           // 0: 64
	MinMisses int           // 0: 500
	MinRatio  float64       // 0: 0.9
	Throttle  bool
	Disable   bool
}

// CacheGroupsConfig controls RFC 9875 cache groups. The zero value honors them.
type CacheGroupsConfig struct{ Ignore bool }

// ClientConfig controls client request directives. HonorRevalidation lets
// no-cache and max-age=0 reach the origin (D5).
type ClientConfig struct{ HonorRevalidation bool }

// WarmConfig tunes Engine.Warm.
type WarmConfig struct{ Concurrency int } // 0: min(4, MaxConcurrent-ReserveForeground); at most that

// TimeoutsConfig bounds origin and store calls (FR-TMO).
type TimeoutsConfig struct {
	Origin     time.Duration // 0: 30s
	Background time.Duration // 0: 30s; background refresh and Warm (FR-TMO-1)
	Store      time.Duration // 0: 50ms (remote stores only)
	StreamIdle time.Duration // 0: 60s (D26)
}

// LimitsConfig bounds request input (FR-VAL-1, FR-VAL-3, FR-STO-10).
type LimitsConfig struct {
	MaxPathBytes        int // 0: 8192
	MaxQueryBytes       int // 0: 8192
	MaxQueryParams      int // 0: 256
	MaxKeyedHeaderBytes int // 0: 1024
	MaxGroups           int // 0: 32
	MaxGroupBytes       int // 0: 128
}

// defaultStatuses is the storable set from 01 §6; 302 and 307 still need
// explicit freshness (D39).
var defaultStatuses = []int{200, 203, 204, 300, 301, 302, 307, 308, 404, 405, 410, 414, 501}

// prepareConfig is the Config half of New: copy, default, canonicalize,
// validate (04 §1.1). It leaves Store nil; New builds the default store.
func prepareConfig(c Config) (Config, error) {
	c.copySlices()
	c.applyDefaults()
	c.canonicalize()
	return c, c.validate()
}

// copySlices detaches every slice from the caller's backing arrays.
func (c *Config) copySlices() {
	for _, p := range []*[]string{
		&c.Key.QueryDrop, &c.Key.QueryKeep, &c.Key.Headers, &c.Key.Cookies,
		&c.Key.VaryAllow, &c.Key.AcceptEncoding, &c.Forward.Allow,
		&c.Bypass.Cookies, &c.Bypass.Headers, &c.Storable.StreamTypes,
	} {
		*p = slices.Clone(*p)
	}
	c.Storable.Statuses = slices.Clone(c.Storable.Statuses)
}

func (c *Config) applyDefaults() {
	orInt(&c.Key.MaxVaryHeaders, 8)
	orInt(&c.Key.MaxVariants, 8)
	if len(c.Key.AcceptEncoding) == 0 {
		c.Key.AcceptEncoding = []string{"gzip"}
	}
	orDur(&c.Bypass.ReportStrippedCookies, 5*time.Minute)
	if c.Storable.Statuses == nil {
		c.Storable.Statuses = slices.Clone(defaultStatuses)
	}
	if c.Storable.MaxObjectBytes == 0 {
		c.Storable.MaxObjectBytes = 1 << 20
	}

	f := &c.Freshness
	switch {
	case f.NoJitter:
		f.Jitter = 0 // consumers read Jitter alone
	case f.Jitter == 0:
		f.Jitter = 0.10
	}
	orDur(&f.JitterMinLifetime, 10*time.Second)
	orFloat(&f.EarlyRefreshBeta, 1.0)
	orFloat(&f.HeuristicFraction, 0.1)
	orDur(&f.HeuristicMax, time.Hour)
	orDur(&f.Keep, 5*time.Minute)

	t := &c.Timeouts
	orDur(&t.Origin, 30*time.Second)
	orDur(&t.Background, 30*time.Second)
	orDur(&t.Store, 50*time.Millisecond)
	orDur(&t.StreamIdle, 60*time.Second)

	// Coalesce defaults follow a short origin timeout instead of failing
	// validation against it; explicit values above it are still rejected.
	coalesceDefault := 10 * time.Second
	if t.Origin > 0 {
		coalesceDefault = min(coalesceDefault, t.Origin)
	}
	orDur(&c.Coalesce.LeaderMaxAge, coalesceDefault)
	orDur(&c.Coalesce.FollowerMaxWait, coalesceDefault)
	orDur(&c.Coalesce.HitForMissTTL, 30*time.Second)

	l := &c.Limiter
	orInt(&l.MaxConcurrent, 64)
	orInt(&l.MaxQueue, 1024)
	orDur(&l.MaxQueueWait, 2*time.Second)
	orInt(&l.MaxPerPartition, 16)
	if l.MaxConcurrent > 0 {
		l.MaxPerPartition = min(l.MaxPerPartition, l.MaxConcurrent)
	}
	if l.ReserveForeground == 0 && l.MaxConcurrent >= 2 {
		l.ReserveForeground = max(1, l.MaxConcurrent/4)
	}
	orInt(&l.MaxUpload, max(1, l.MaxConcurrent/4))

	b := &c.Breaker
	orDur(&b.Window, 10*time.Second)
	orInt(&b.MinRequests, 20)
	orFloat(&b.FailureRatio, 0.5)
	orDur(&b.OpenFor, 5*time.Second)
	orDur(&b.MaxOpenFor, max(60*time.Second, b.OpenFor)) // FR-CB-3: never below the first open
	orInt(&b.HalfOpenProbes, 1)

	orDur(&c.Negative.TTL, 2*time.Second)

	m := &c.MissRate
	orDur(&m.Window, 10*time.Second)
	orInt(&m.TopK, 64)
	orInt(&m.MinMisses, 500)
	orFloat(&m.MinRatio, 0.9)

	// More warm workers than slots outside the reserve can never fetch at
	// once; they would only hold queue places (FR-WRM-1).
	orInt(&c.Warm.Concurrency, min(4, max(1, l.MaxConcurrent-l.ReserveForeground)))

	lim := &c.Limits
	orInt(&lim.MaxPathBytes, 8192)
	orInt(&lim.MaxQueryBytes, 8192)
	orInt(&lim.MaxQueryParams, 256)
	orInt(&lim.MaxKeyedHeaderBytes, 1024)
	orInt(&lim.MaxGroups, 32)
	orInt(&lim.MaxGroupBytes, 128)

	if c.CacheStatus == "" {
		c.CacheStatus = "Weir"
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	if c.Rand == nil {
		c.Rand = rand.Float64
	}
}

// canonicalize rewrites names to the form requests are matched in and drops
// repeats, first occurrence first, so each input is keyed and forwarded once.
func (c *Config) canonicalize() {
	for _, p := range []*[]string{&c.Key.Headers, &c.Key.VaryAllow, &c.Forward.Allow, &c.Bypass.Headers} {
		*p = dedupe(*p, http.CanonicalHeaderKey)
	}
	c.Key.AcceptEncoding = dedupe(c.Key.AcceptEncoding, strings.ToLower)
	c.Key.Cookies = dedupe(c.Key.Cookies, nil)       // cookie names are case-sensitive
	c.Bypass.Cookies = dedupe(c.Bypass.Cookies, nil) // cookie names are case-sensitive
	// RFC 9110 §8.3.1: a media type's type and subtype compare case-insensitively.
	c.Storable.StreamTypes = dedupe(c.Storable.StreamTypes, strings.ToLower)
}

// dedupe applies norm (when non-nil) to each element in place and removes
// later repeats. The config is bounded by what the operator wrote.
func dedupe(s []string, norm func(string) string) []string {
	out := s[:0]
	for _, v := range s {
		if norm != nil {
			v = norm(v)
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// validate applies the FR-LCY-1 rules that need no store. The store-size rule
// (MaxObjectBytes against store.MaxObjectBytes) runs in New once the store
// exists.
func (c *Config) validate() error {
	for _, f := range []struct {
		name string
		v    int64
	}{
		{"Key.MaxVaryHeaders", int64(c.Key.MaxVaryHeaders)},
		{"Key.MaxVariants", int64(c.Key.MaxVariants)},
		{"Storable.MaxObjectBytes", c.Storable.MaxObjectBytes},
		{"Freshness.JitterMinLifetime", int64(c.Freshness.JitterMinLifetime)},
		{"Freshness.HeuristicMax", int64(c.Freshness.HeuristicMax)},
		{"Freshness.DefaultTTL", int64(c.Freshness.DefaultTTL)},
		{"Freshness.DefaultStaleWhileRevalidate", int64(c.Freshness.DefaultStaleWhileRevalidate)},
		{"Freshness.DefaultStaleIfError", int64(c.Freshness.DefaultStaleIfError)},
		{"Freshness.Keep", int64(c.Freshness.Keep)},
		{"Coalesce.LeaderMaxAge", int64(c.Coalesce.LeaderMaxAge)},
		{"Coalesce.FollowerMaxWait", int64(c.Coalesce.FollowerMaxWait)},
		{"Coalesce.HitForMissTTL", int64(c.Coalesce.HitForMissTTL)},
		{"Limiter.MaxConcurrent", int64(c.Limiter.MaxConcurrent)},
		{"Limiter.MaxQueue", int64(c.Limiter.MaxQueue)},
		{"Limiter.MaxQueueWait", int64(c.Limiter.MaxQueueWait)},
		{"Limiter.MaxPerPartition", int64(c.Limiter.MaxPerPartition)},
		{"Limiter.ReserveForeground", int64(c.Limiter.ReserveForeground)},
		{"Limiter.MaxPerHost", int64(c.Limiter.MaxPerHost)},
		{"Limiter.MaxUpload", int64(c.Limiter.MaxUpload)},
		{"Breaker.Window", int64(c.Breaker.Window)},
		{"Breaker.MinRequests", int64(c.Breaker.MinRequests)},
		{"Breaker.OpenFor", int64(c.Breaker.OpenFor)},
		{"Breaker.MaxOpenFor", int64(c.Breaker.MaxOpenFor)},
		{"Breaker.HalfOpenProbes", int64(c.Breaker.HalfOpenProbes)},
		{"Negative.TTL", int64(c.Negative.TTL)},
		{"MissRate.Window", int64(c.MissRate.Window)},
		{"MissRate.TopK", int64(c.MissRate.TopK)},
		{"MissRate.MinMisses", int64(c.MissRate.MinMisses)},
		{"Warm.Concurrency", int64(c.Warm.Concurrency)},
		{"Timeouts.Origin", int64(c.Timeouts.Origin)},
		{"Timeouts.Background", int64(c.Timeouts.Background)},
		{"Timeouts.Store", int64(c.Timeouts.Store)},
		{"Timeouts.StreamIdle", int64(c.Timeouts.StreamIdle)},
		{"Limits.MaxPathBytes", int64(c.Limits.MaxPathBytes)},
		{"Limits.MaxQueryBytes", int64(c.Limits.MaxQueryBytes)},
		{"Limits.MaxQueryParams", int64(c.Limits.MaxQueryParams)},
		{"Limits.MaxKeyedHeaderBytes", int64(c.Limits.MaxKeyedHeaderBytes)},
		{"Limits.MaxGroups", int64(c.Limits.MaxGroups)},
		{"Limits.MaxGroupBytes", int64(c.Limits.MaxGroupBytes)},
	} {
		if f.v < 0 {
			return invalid(f.name, "negative")
		}
	}
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"Freshness.EarlyRefreshBeta", c.Freshness.EarlyRefreshBeta},
		{"Freshness.HeuristicFraction", c.Freshness.HeuristicFraction},
		{"MissRate.MinRatio", c.MissRate.MinRatio},
	} {
		if !(f.v >= 0) || math.IsInf(f.v, 1) { // NaN fails every comparison
			return invalid(f.name, "negative or not finite")
		}
	}
	if j := c.Freshness.Jitter; !(j >= 0 && j <= 0.5) {
		return invalid("Freshness.Jitter", "outside [0, 0.5]")
	}
	if r := c.Breaker.FailureRatio; !(r > 0 && r <= 1) {
		return invalid("Breaker.FailureRatio", "outside (0, 1]")
	}
	if c.Key.Vary > VaryStrict {
		return invalid("Key.Vary", "unknown mode")
	}
	if c.Forward.Mode > ForwardAll {
		return invalid("Forward.Mode", "unknown mode")
	}
	if l := &c.Limiter; l.ReserveForeground > 0 && l.ReserveForeground >= l.MaxConcurrent {
		// No slot would be left for background refresh or Warm (FR-LIM-4).
		return invalid("Limiter.ReserveForeground", "not below Limiter.MaxConcurrent")
	}
	if l := &c.Limiter; c.Warm.Concurrency > max(1, l.MaxConcurrent-l.ReserveForeground) {
		return invalid("Warm.Concurrency", "above Limiter.MaxConcurrent - Limiter.ReserveForeground")
	}
	if c.Coalesce.LeaderMaxAge > c.Timeouts.Origin {
		return invalid("Coalesce.LeaderMaxAge", "above Timeouts.Origin")
	}
	if c.Coalesce.FollowerMaxWait > c.Timeouts.Origin {
		return invalid("Coalesce.FollowerMaxWait", "above Timeouts.Origin")
	}
	if c.Breaker.MaxOpenFor < c.Breaker.OpenFor {
		// A reopen would be shorter than the first open (FR-CB-3).
		return invalid("Breaker.MaxOpenFor", "below Breaker.OpenFor")
	}
	for _, s := range c.Storable.Statuses {
		switch {
		case s < 200 || s > 599:
			return invalid("Storable.Statuses", fmt.Sprintf("%d is not a final status code", s))
		case s == 206 || s == 304 || s == 500 || s == 502 || s == 503 || s == 504:
			// These have dedicated handling and are never stored as entries.
			return invalid("Storable.Statuses", fmt.Sprintf("%d cannot be stored", s))
		}
	}
	// CacheStatus becomes an RFC 9211 member name, so it must be an sf-token
	// (RFC 9651 §3.3.4) or every response carries a malformed field.
	if !isSFToken(c.CacheStatus) {
		return invalid("CacheStatus", "not a structured-field token")
	}
	for _, f := range []struct {
		name  string
		names []string
	}{
		// A header name that is not a token never matches a request header,
		// so the entry would silently do nothing (for example "Authorization ").
		{"Key.Headers", c.Key.Headers},
		{"Key.VaryAllow", c.Key.VaryAllow},
		{"Forward.Allow", c.Forward.Allow},
		{"Bypass.Headers", c.Bypass.Headers},
		// T-3: a separator in a cookie name would split into extra cookies in
		// the rewritten Cookie header, forwarding a pair that is not keyed.
		{"Key.Cookies", c.Key.Cookies},
		{"Bypass.Cookies", c.Bypass.Cookies},
		// T-13: the chosen coding becomes the forwarded Accept-Encoding value.
		{"Key.AcceptEncoding", c.Key.AcceptEncoding},
	} {
		for _, n := range f.names {
			if !isToken(n) {
				return invalid(f.name, fmt.Sprintf("%q is not a token", n))
			}
		}
	}
	for _, n := range c.Forward.Allow {
		// T-1, FR-FWD-1: a keyed field goes in its normalized form and a
		// hop-by-hop field never goes, so the entry could only mislead.
		switch {
		case slices.Contains(c.Key.Headers, n), n == "Cookie" && len(c.Key.Cookies) > 0:
			return invalid("Forward.Allow", fmt.Sprintf("%q is keyed and already forwarded", n))
		case keys.IsHopByHop(n):
			return invalid("Forward.Allow", fmt.Sprintf("%q is hop-by-hop and never forwarded", n))
		}
	}
	// A typed nil passes a nil interface check and panics on first use.
	if isTypedNil(c.Observer) {
		return invalid("Observer", "typed nil")
	}
	if isTypedNil(c.Store) {
		return invalid("Store", "typed nil")
	}
	return nil
}

// isTypedNil reports whether v is a non-nil interface holding a nil value.
func isTypedNil(v any) bool {
	if v == nil {
		return false
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Chan, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

func invalid(field, reason string) error {
	return fmt.Errorf("%w: field %s: %s", ErrInvalidConfig, field, reason)
}

// isToken reports whether s is an RFC 9110 §5.6.2 token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isTchar(s[i]) {
			return false
		}
	}
	return true
}

func isTchar(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

// isSFToken reports whether s is an RFC 9651 §3.3.4 sf-token:
// ( ALPHA / "*" ) *( tchar / ":" / "/" ).
func isSFToken(s string) bool {
	if s == "" {
		return false
	}
	if c := s[0]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '*' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isTchar(s[i]) && s[i] != ':' && s[i] != '/' {
			return false
		}
	}
	return true
}

func orInt(p *int, d int) {
	if *p == 0 {
		*p = d
	}
}

func orDur(p *time.Duration, d time.Duration) {
	if *p == 0 {
		*p = d
	}
}

func orFloat(p *float64, d float64) {
	if *p == 0 {
		*p = d
	}
}
