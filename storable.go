package weir

import (
	"net/http"
	"slices"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/internal/sfv"
)

// storeDecision is the outcome of storability. It carries the parsed
// directives and the unjittered lifetime so buildEntry does not parse twice.
type storeDecision struct {
	ok     bool
	reason string // EvNotStored reason when !ok (04 §9)
	// responseDriven reports that the response, not the request's unkeyed
	// inputs, made it unstorable; only then may a hit-for-miss marker be
	// written (FR-STO-12, T-31).
	responseDriven bool
	cc             httpcc.ResponseDirectives
	lifetime       time.Duration
	heuristic      bool
	varyNames      []string // keys.VaryNames of the response's Vary (FR-KEY-7)
	groups         []string // distinct Cache-Groups members (FR-STO-10)
}

// storability applies FR-STO-1 to FR-STO-10 to a fully read response
// received at respTime.
func storability(cfg *Config, c *keys.Classified, resp *Response, body []byte, respTime time.Time) storeDecision {
	d := storeDecision{cc: httpcc.ParseResponse(resp.Header)}
	d.lifetime, d.heuristic = httpcc.Lifetime(d.cc, resp.Header, resp.StatusCode, respTime, cfg.freshness())
	fail := func(reason string, responseDriven bool) storeDecision {
		// T-31: under unkeyed inputs no reason is response-driven, whichever
		// check happened to fail first.
		d.reason = reason
		d.responseDriven = responseDriven && !c.Authorized && !c.ReqCC.NoStore && !c.Unkeyed
		return d
	}
	h := resp.Header
	var star bool
	d.varyNames, star = keys.VaryNames(h["Vary"])
	statusOK := slices.Contains(cfg.Storable.Statuses, resp.StatusCode)
	// CacheGroups.Ignore does not apply here: it switches off
	// Cache-Group-Invalidation only, so an operator Purge by group still works.
	var groupsErr error
	d.groups, groupsErr = groupList(cfg, h["Cache-Groups"])
	switch {
	case c.Forwarded.Method != http.MethodGet:
		return fail("method", false)
	case !statusOK:
		return fail("status", true) // T-7
	case c.ReqCC.NoStore:
		return fail("no-store", false)
	case d.cc.NoStore && !d.cc.MustUnderstand: // RFC 9111 §5.2.2.3
		return fail("no-store", true)
	case d.cc.Private:
		return fail("private", true)
	case c.Authorized && (d.cc.Malformed || !d.cc.Public && !validSMaxAge(&d.cc) && !d.cc.MustRevalidate):
		// RFC 9111 §3.5, T-8. Request-driven: the credentials are unkeyed.
		return fail("authorization", false)
	case len(h["Set-Cookie"]) > 0 && !cfg.Storable.StripSetCookie:
		return fail("set-cookie", true) // T-8
	case star: // FR-KEY-8
		return fail("vary-star", true)
	case len(d.varyNames) > cfg.Key.MaxVaryHeaders: // FR-KEY-10
		return fail("vary-too-many", true)
	case varyRefused(cfg, d.varyNames, false):
		return fail("vary-sensitive", true)
	case cfg.Key.Vary == VaryStrict && varyRefused(cfg, d.varyNames, true):
		return fail("vary-strict", true)
	case !hasFreshness(d, h, resp.StatusCode):
		return fail("no-freshness", true) // T-6: never inferred from the path
	case int64(len(body))+headerBytes(h) > cfg.Storable.MaxObjectBytes:
		return fail("too-large", true)
	case groupsErr != nil:
		// FR-STO-10, T-21: stored without its groups, a purge would miss it.
		return fail("groups", true)
	}
	d.ok = true
	return d
}

// groupList parses the lines of a Cache-Groups or Cache-Group-Invalidation
// field under Limits (FR-STO-10) and returns each distinct name once, in
// byte order: equal names give equal tags, and a repeated tag only costs
// entry size and epoch lookups.
func groupList(cfg *Config, lines []string) ([]string, error) {
	g, err := sfv.ParseStringList(lines, cfg.Limits.MaxGroups, cfg.Limits.MaxGroupBytes)
	slices.Sort(g)
	return slices.Compact(g), err
}

// varyRefused reports a Vary name that Key.VaryAllow does not list and that
// the policy refuses: a sensitive name in any mode, any name under
// VaryStrict (FR-KEY-9, T-15).
func varyRefused(cfg *Config, names []string, strict bool) bool {
	for _, n := range names {
		sensitive := n == "Cookie" || n == "Authorization" || n == "Proxy-Authorization"
		if (strict || sensitive) && !slices.Contains(cfg.Key.VaryAllow, n) {
			return true
		}
	}
	return false
}

// validSMaxAge reports an s-maxage that can grant the RFC 9111 §3.5
// permission (FR-STO-5). T-8: under an unusable field the lifetime is zero
// (FR-FRS-2), and a zero-lifetime entry could be served stale to another user.
func validSMaxAge(cc *httpcc.ResponseDirectives) bool {
	return cc.SMaxAge.Set && !cc.Unusable()
}

// hasFreshness is FR-STO-8. 302 and 307 need explicit freshness (D39).
func hasFreshness(d storeDecision, h http.Header, status int) bool {
	explicit := d.cc.SMaxAge.Set || d.cc.MaxAge.Set || len(h["Expires"]) > 0 && !d.cc.Targeted // FR-TCC-2
	if status == http.StatusFound || status == http.StatusTemporaryRedirect {
		return explicit
	}
	// A validator stores the response with any lifetime, zero included, so
	// the "together with no-cache or a zero lifetime" clause needs no test.
	return explicit || (d.heuristic && d.lifetime > 0) || d.cc.Public ||
		len(h["Etag"]) > 0 || len(h["Last-Modified"]) > 0
}

// headerBytes counts header bytes as store.Entry.Size does.
func headerBytes(h http.Header) int64 {
	var n int64
	for name, vals := range h {
		for _, v := range vals {
			n += int64(len(name) + len(v))
		}
	}
	return n
}

// freshness returns the httpcc view of the defaulted Freshness settings.
func (c *Config) freshness() httpcc.Config {
	f := &c.Freshness
	return httpcc.Config{
		HeuristicFraction: f.HeuristicFraction,
		HeuristicMax:      f.HeuristicMax,
		DefaultTTL:        f.DefaultTTL,
		DefaultSWR:        f.DefaultStaleWhileRevalidate,
		DefaultSIE:        f.DefaultStaleIfError,
	}
}
