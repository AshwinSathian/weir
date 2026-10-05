package weir

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

type nilObserver struct{}

func (*nilObserver) Observe(Event) {}

// FR-LCY-1: the zero Config is valid and yields every 01 §6 default except the
// store, which New builds (P0-05, M1-09).
func TestZeroConfigValid(t *testing.T) {
	c, err := prepareConfig(Config{})
	if err != nil {
		t.Fatalf("prepareConfig(Config{}) = %v", err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"Key.MaxVaryHeaders", c.Key.MaxVaryHeaders, 8},
		{"Key.MaxVariants", c.Key.MaxVariants, 8},
		{"Key.AcceptEncoding", strings.Join(c.Key.AcceptEncoding, ","), "gzip"},
		{"Key.Vary", c.Key.Vary, VaryAuto},
		{"Forward.Mode", c.Forward.Mode, ForwardStrict},
		{"Bypass.ReportStrippedCookies", c.Bypass.ReportStrippedCookies, 5 * time.Minute},
		{"Storable.Statuses", fmt.Sprint(c.Storable.Statuses), "[200 203 204 300 301 302 307 308 404 405 410 414 501]"},
		{"Storable.MaxObjectBytes", c.Storable.MaxObjectBytes, int64(1 << 20)},
		{"Freshness.Jitter", c.Freshness.Jitter, 0.10},
		{"Freshness.JitterMinLifetime", c.Freshness.JitterMinLifetime, 10 * time.Second},
		{"Freshness.EarlyRefreshBeta", c.Freshness.EarlyRefreshBeta, 1.0},
		{"Freshness.HeuristicFraction", c.Freshness.HeuristicFraction, 0.1},
		{"Freshness.HeuristicMax", c.Freshness.HeuristicMax, time.Hour},
		{"Freshness.DefaultTTL", c.Freshness.DefaultTTL, time.Duration(0)},
		{"Freshness.Keep", c.Freshness.Keep, 5 * time.Minute},
		{"Coalesce.LeaderMaxAge", c.Coalesce.LeaderMaxAge, 10 * time.Second},
		{"Coalesce.FollowerMaxWait", c.Coalesce.FollowerMaxWait, 10 * time.Second},
		{"Coalesce.HitForMissTTL", c.Coalesce.HitForMissTTL, 30 * time.Second},
		{"Limiter.MaxConcurrent", c.Limiter.MaxConcurrent, 64},
		{"Limiter.MaxQueue", c.Limiter.MaxQueue, 1024},
		{"Limiter.MaxQueueWait", c.Limiter.MaxQueueWait, 2 * time.Second},
		{"Limiter.MaxPerPartition", c.Limiter.MaxPerPartition, 16},
		{"Limiter.ReserveForeground", c.Limiter.ReserveForeground, 16},
		{"Limiter.MaxUpload", c.Limiter.MaxUpload, 16},
		{"Limiter.MaxPerHost", c.Limiter.MaxPerHost, 0},
		{"Breaker.Window", c.Breaker.Window, 10 * time.Second},
		{"Breaker.MinRequests", c.Breaker.MinRequests, 20},
		{"Breaker.FailureRatio", c.Breaker.FailureRatio, 0.5},
		{"Breaker.OpenFor", c.Breaker.OpenFor, 5 * time.Second},
		{"Breaker.MaxOpenFor", c.Breaker.MaxOpenFor, 60 * time.Second},
		{"Breaker.HalfOpenProbes", c.Breaker.HalfOpenProbes, 1},
		{"Negative.TTL", c.Negative.TTL, 2 * time.Second},
		{"MissRate.Window", c.MissRate.Window, 10 * time.Second},
		{"MissRate.TopK", c.MissRate.TopK, 64},
		{"MissRate.MinMisses", c.MissRate.MinMisses, 500},
		{"MissRate.MinRatio", c.MissRate.MinRatio, 0.9},
		{"Timeouts.Origin", c.Timeouts.Origin, 30 * time.Second},
		{"Timeouts.Background", c.Timeouts.Background, 30 * time.Second},
		{"Timeouts.Store", c.Timeouts.Store, 50 * time.Millisecond},
		{"Timeouts.StreamIdle", c.Timeouts.StreamIdle, 60 * time.Second},
		{"Warm.Concurrency", c.Warm.Concurrency, 4},
		{"Limits.MaxPathBytes", c.Limits.MaxPathBytes, 8192},
		{"Limits.MaxQueryBytes", c.Limits.MaxQueryBytes, 8192},
		{"Limits.MaxQueryParams", c.Limits.MaxQueryParams, 256},
		{"Limits.MaxKeyedHeaderBytes", c.Limits.MaxKeyedHeaderBytes, 1024},
		{"Limits.MaxGroups", c.Limits.MaxGroups, 32},
		{"Limits.MaxGroupBytes", c.Limits.MaxGroupBytes, 128},
		{"CacheStatus", c.CacheStatus, "Weir"},
		{"Logger set", c.Logger != nil, true},
		{"Rand set", c.Rand != nil, true},
	}
	for _, ck := range checks {
		if ck.got != ck.want {
			t.Errorf("%s = %v, want %v", ck.name, ck.got, ck.want)
		}
	}
	if r := c.Rand(); r < 0 || r >= 1 {
		t.Errorf("Rand() = %v, want [0, 1)", r)
	}
}

// FR-LCY-1, FR-LIM-7, D25: limiter defaults derived from MaxConcurrent.
func TestLimiterDerivedDefaults(t *testing.T) {
	tests := []struct {
		name                                   string
		maxConcurrent, perPartition            int
		wantPartition, wantReserve, wantUpload int
	}{
		{"quarter of max concurrent", 40, 0, 16, 10, 10},
		{"per-partition clamped to max concurrent", 4, 0, 4, 1, 1},
		{"explicit per-partition clamped", 8, 100, 8, 2, 2},
		{"reserve at least one from two slots", 2, 0, 2, 1, 1},
		{"single slot reserves nothing, uploads keep one", 1, 0, 1, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := prepareConfig(Config{Limiter: LimiterConfig{MaxConcurrent: tt.maxConcurrent, MaxPerPartition: tt.perPartition}})
			if err != nil {
				t.Fatal(err)
			}
			l := c.Limiter
			if l.MaxPerPartition != tt.wantPartition || l.ReserveForeground != tt.wantReserve || l.MaxUpload != tt.wantUpload {
				t.Errorf("got partition %d reserve %d upload %d, want %d %d %d",
					l.MaxPerPartition, l.ReserveForeground, l.MaxUpload, tt.wantPartition, tt.wantReserve, tt.wantUpload)
			}
		})
	}
}

// FR-WRM-1, FR-LCY-1: the Warm.Concurrency default is lowered to the slots
// outside the reserve, so a small limiter stays valid; an explicit value
// equal to them is kept.
func TestWarmConcurrencyDefault(t *testing.T) {
	tests := []struct {
		name                    string
		maxConcurrent, explicit int
		want                    int
	}{
		{"default limiter", 0, 0, 4},
		{"lowered to unreserved slots", 4, 0, 3},
		{"two slots leave one", 2, 0, 1},
		{"single slot", 1, 0, 1},
		{"explicit at the bound", 8, 6, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := prepareConfig(Config{Limiter: LimiterConfig{MaxConcurrent: tt.maxConcurrent}, Warm: WarmConfig{Concurrency: tt.explicit}})
			if err != nil {
				t.Fatal(err)
			}
			if c.Warm.Concurrency != tt.want {
				t.Errorf("Warm.Concurrency = %d, want %d", c.Warm.Concurrency, tt.want)
			}
		})
	}
}

// FR-LCY-1: each rule that needs no store rejects with ErrInvalidConfig and
// names the field.
func TestInvalidConfigRejected(t *testing.T) {
	var typedNil *nilObserver
	tests := []struct {
		name  string
		cfg   Config
		field string
	}{
		{"negative max object bytes", Config{Storable: StorableConfig{MaxObjectBytes: -1}}, "Storable.MaxObjectBytes"},
		{"negative max variants", Config{Key: KeyConfig{MaxVariants: -1}}, "Key.MaxVariants"},
		{"negative max concurrent", Config{Limiter: LimiterConfig{MaxConcurrent: -1}}, "Limiter.MaxConcurrent"},
		{"negative max queue", Config{Limiter: LimiterConfig{MaxQueue: -5}}, "Limiter.MaxQueue"},
		{"negative max upload", Config{Limiter: LimiterConfig{MaxUpload: -1}}, "Limiter.MaxUpload"},
		{"negative keep", Config{Freshness: FreshnessConfig{Keep: -time.Second}}, "Freshness.Keep"},
		{"negative default ttl", Config{Freshness: FreshnessConfig{DefaultTTL: -time.Second}}, "Freshness.DefaultTTL"},
		{"negative origin timeout", Config{Timeouts: TimeoutsConfig{Origin: -time.Second}}, "Timeouts.Origin"},
		{"negative warm concurrency", Config{Warm: WarmConfig{Concurrency: -1}}, "Warm.Concurrency"},
		{"reserve equal to max concurrent", Config{Limiter: LimiterConfig{MaxConcurrent: 4, ReserveForeground: 4}}, "Limiter.ReserveForeground"},
		{"reserve on a single slot", Config{Limiter: LimiterConfig{MaxConcurrent: 1, ReserveForeground: 1}}, "Limiter.ReserveForeground"},
		{"warm concurrency above unreserved slots", Config{Limiter: LimiterConfig{MaxConcurrent: 8, ReserveForeground: 2}, Warm: WarmConfig{Concurrency: 7}}, "Warm.Concurrency"},
		{"negative path limit", Config{Limits: LimitsConfig{MaxPathBytes: -1}}, "Limits.MaxPathBytes"},
		{"breaker max open below open", Config{Breaker: BreakerConfig{OpenFor: 10 * time.Second, MaxOpenFor: 5 * time.Second}}, "Breaker.MaxOpenFor"},
		{"leader max age above origin timeout", Config{Coalesce: CoalesceConfig{LeaderMaxAge: 31 * time.Second}}, "Coalesce.LeaderMaxAge"},
		{"follower wait above origin timeout", Config{Coalesce: CoalesceConfig{LeaderMaxAge: time.Second, FollowerMaxWait: 5 * time.Second}, Timeouts: TimeoutsConfig{Origin: 2 * time.Second}}, "Coalesce.FollowerMaxWait"},
		{"jitter above 0.5", Config{Freshness: FreshnessConfig{Jitter: 0.51}}, "Freshness.Jitter"},
		{"negative jitter", Config{Freshness: FreshnessConfig{Jitter: -0.1}}, "Freshness.Jitter"},
		{"failure ratio above 1", Config{Breaker: BreakerConfig{FailureRatio: 1.01}}, "Breaker.FailureRatio"},
		{"negative failure ratio", Config{Breaker: BreakerConfig{FailureRatio: -0.5}}, "Breaker.FailureRatio"},
		{"status 206 storable", Config{Storable: StorableConfig{Statuses: []int{200, 206}}}, "Storable.Statuses"},
		{"status 304 storable", Config{Storable: StorableConfig{Statuses: []int{304}}}, "Storable.Statuses"},
		{"status 500 storable", Config{Storable: StorableConfig{Statuses: []int{500}}}, "Storable.Statuses"},
		{"status 502 storable", Config{Storable: StorableConfig{Statuses: []int{502}}}, "Storable.Statuses"},
		{"status 503 storable", Config{Storable: StorableConfig{Statuses: []int{503}}}, "Storable.Statuses"},
		{"status 504 storable", Config{Storable: StorableConfig{Statuses: []int{504}}}, "Storable.Statuses"},
		{"status out of range", Config{Storable: StorableConfig{Statuses: []int{99}}}, "Storable.Statuses"},
		{"NaN jitter", Config{Freshness: FreshnessConfig{Jitter: math.NaN()}}, "Freshness.Jitter"},
		{"NaN failure ratio", Config{Breaker: BreakerConfig{FailureRatio: math.NaN()}}, "Breaker.FailureRatio"},
		{"infinite early refresh beta", Config{Freshness: FreshnessConfig{EarlyRefreshBeta: math.Inf(1)}}, "Freshness.EarlyRefreshBeta"},
		{"negative heuristic fraction", Config{Freshness: FreshnessConfig{HeuristicFraction: -0.1}}, "Freshness.HeuristicFraction"},
		{"unknown vary mode", Config{Key: KeyConfig{Vary: VaryStrict + 1}}, "Key.Vary"},
		{"unknown forward mode", Config{Forward: ForwardConfig{Mode: ForwardAll + 1}}, "Forward.Mode"},
		{"cache status with space", Config{CacheStatus: "My Cache"}, "CacheStatus"},
		{"cache status with comma", Config{CacheStatus: "a,b"}, "CacheStatus"},
		{"cache status starting with digit", Config{CacheStatus: "1cache"}, "CacheStatus"},
		{"keyed header with trailing space", Config{Key: KeyConfig{Headers: []string{"X-Tenant "}}}, "Key.Headers"},
		{"empty vary allow name", Config{Key: KeyConfig{VaryAllow: []string{""}}}, "Key.VaryAllow"},
		{"forward allow with colon", Config{Forward: ForwardConfig{Allow: []string{"X-A:"}}}, "Forward.Allow"},
		{"bypass header with trailing space", Config{Bypass: BypassConfig{Headers: []string{"authorization "}}}, "Bypass.Headers"},
		{"keyed cookie with separator", Config{Key: KeyConfig{Cookies: []string{"a;b"}}}, "Key.Cookies"},
		{"keyed cookie with equals", Config{Key: KeyConfig{Cookies: []string{"a=b"}}}, "Key.Cookies"},
		{"empty bypass cookie", Config{Bypass: BypassConfig{Cookies: []string{""}}}, "Bypass.Cookies"},
		{"accept-encoding list in one token", Config{Key: KeyConfig{AcceptEncoding: []string{"gzip, br"}}}, "Key.AcceptEncoding"},
		{"empty accept-encoding token", Config{Key: KeyConfig{AcceptEncoding: []string{"gzip", ""}}}, "Key.AcceptEncoding"},
		{"informational status storable", Config{Storable: StorableConfig{Statuses: []int{101}}}, "Storable.Statuses"},
		{"typed nil observer", Config{Observer: typedNil}, "Observer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prepareConfig(tt.cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), "field "+tt.field+":") {
				t.Errorf("err = %q, want it to name field %s", err, tt.field)
			}
		})
	}
}

// Negative ReportStrippedCookies disables the report (FR-OBS-5); it is the one
// negative duration that is valid.
func TestReportStrippedCookiesNegativeDisables(t *testing.T) {
	c, err := prepareConfig(Config{Bypass: BypassConfig{ReportStrippedCookies: -1}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Bypass.ReportStrippedCookies >= 0 {
		t.Errorf("ReportStrippedCookies = %v, want negative (disabled)", c.Bypass.ReportStrippedCookies)
	}
}

// FR-LCY-1, FR-FWD-1: configured header names are canonicalized so they match
// request headers, which net/http canonicalizes.
func TestConfigHeaderNamesCanonicalized(t *testing.T) {
	c, err := prepareConfig(Config{
		Key:     KeyConfig{Headers: []string{"x-tenant"}, VaryAllow: []string{"accept-language"}, AcceptEncoding: []string{"BR", "Gzip"}},
		Forward: ForwardConfig{Allow: []string{"x-debug-id"}},
		Bypass:  BypassConfig{Headers: []string{"authorization"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range []struct{ got, want []string }{
		{c.Key.Headers, []string{"X-Tenant"}},
		{c.Key.VaryAllow, []string{"Accept-Language"}},
		{c.Forward.Allow, []string{"X-Debug-Id"}},
		{c.Bypass.Headers, []string{"Authorization"}},
		{c.Key.AcceptEncoding, []string{"br", "gzip"}},
	} {
		if !slices.Equal(ck.got, ck.want) {
			t.Errorf("got %q, want %q", ck.got, ck.want)
		}
	}
}

// FR-LCY-1: the prepared Config never aliases caller slices (04 §1.1).
func TestConfigCopiesCallerSlices(t *testing.T) {
	in := Config{
		Key:      KeyConfig{QueryDrop: []string{"utm_*"}, QueryKeep: []string{"q"}, Headers: []string{"X-A"}, Cookies: []string{"b", "a"}, VaryAllow: []string{"Cookie"}, AcceptEncoding: []string{"gzip"}},
		Forward:  ForwardConfig{Allow: []string{"X-B"}},
		Bypass:   BypassConfig{Cookies: []string{"session"}, Headers: []string{"X-C"}},
		Storable: StorableConfig{Statuses: []int{200}},
	}
	c, err := prepareConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Key.QueryDrop[0], in.Key.QueryKeep[0], in.Key.Headers[0] = "x", "x", "x"
	in.Key.Cookies[0], in.Key.VaryAllow[0], in.Key.AcceptEncoding[0] = "x", "x", "x"
	in.Forward.Allow[0], in.Bypass.Cookies[0], in.Bypass.Headers[0] = "x", "x", "x"
	in.Storable.Statuses[0] = 1
	for _, s := range [][]string{c.Key.QueryDrop, c.Key.QueryKeep, c.Key.Headers, c.Key.Cookies, c.Key.VaryAllow, c.Key.AcceptEncoding, c.Forward.Allow, c.Bypass.Cookies, c.Bypass.Headers} {
		if s[0] == "x" {
			t.Errorf("prepared slice %q aliases the caller's", s)
		}
	}
	if c.Storable.Statuses[0] != 200 {
		t.Error("Storable.Statuses aliases the caller's slice")
	}
	if !slices.Equal(c.Key.Cookies, []string{"b", "a"}) {
		t.Errorf("Key.Cookies = %q, want caller order kept", c.Key.Cookies)
	}
}

// FR-LCY-1, FR-STO-2: an empty non-nil Statuses list means "store nothing",
// not the default set.
func TestEmptyStatusesKept(t *testing.T) {
	c, err := prepareConfig(Config{Storable: StorableConfig{Statuses: []int{}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Storable.Statuses == nil || len(c.Storable.Statuses) != 0 {
		t.Errorf("Statuses = %v, want empty non-nil", c.Storable.Statuses)
	}
}

// FR-LCY-1, FR-FRS-5: NoJitter forces Jitter to zero, even over an explicit
// value, so consumers reading only Jitter still honor it.
func TestNoJitterKeepsZero(t *testing.T) {
	c, err := prepareConfig(Config{Freshness: FreshnessConfig{NoJitter: true, Jitter: 0.3}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Freshness.Jitter != 0 {
		t.Errorf("Jitter = %v, want 0", c.Freshness.Jitter)
	}
}

// FR-LCY-1: a short origin timeout pulls the coalesce defaults down with it;
// only explicit values above it are rejected.
func TestShortOriginTimeoutClampsCoalesceDefaults(t *testing.T) {
	c, err := prepareConfig(Config{Timeouts: TimeoutsConfig{Origin: 5 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Coalesce.LeaderMaxAge != 5*time.Second || c.Coalesce.FollowerMaxWait != 5*time.Second {
		t.Errorf("LeaderMaxAge %v FollowerMaxWait %v, want 5s each", c.Coalesce.LeaderMaxAge, c.Coalesce.FollowerMaxWait)
	}
}

// FR-LCY-1, T-3: names that canonicalize to the same header, and repeated
// cookie or coding names, appear once so the key and forwarded request list
// each input once. First occurrence wins, keeping the operator's order.
func TestConfigDuplicateNamesRemoved(t *testing.T) {
	c, err := prepareConfig(Config{
		Key: KeyConfig{
			Headers:        []string{"x-a", "X-B", "X-A"},
			Cookies:        []string{"b", "a", "b"},
			AcceptEncoding: []string{"br", "gzip", "BR"},
		},
		Forward: ForwardConfig{Allow: []string{"x-debug", "X-Debug"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range []struct{ got, want []string }{
		{c.Key.Headers, []string{"X-A", "X-B"}},
		{c.Key.Cookies, []string{"b", "a"}},
		{c.Key.AcceptEncoding, []string{"br", "gzip"}},
		{c.Forward.Allow, []string{"X-Debug"}},
	} {
		if !slices.Equal(ck.got, ck.want) {
			t.Errorf("got %q, want %q", ck.got, ck.want)
		}
	}
}

// FR-CB-3: a default that depends on another field follows that field
// instead of failing validation against it.
func TestDependentDefaultsFollow(t *testing.T) {
	c, err := prepareConfig(Config{Breaker: BreakerConfig{OpenFor: 2 * time.Minute}})
	if err != nil || c.Breaker.MaxOpenFor != 2*time.Minute {
		t.Errorf("open for 2m: MaxOpenFor = %v, err %v; want 2m", c.Breaker.MaxOpenFor, err)
	}
}
