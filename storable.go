package weir

import (
	"net/http"
	"slices"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
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
}

// storability applies FR-STO-1 to FR-STO-9 to a fully read response received
// at respTime. Cache-Groups limits (FR-STO-10) join in M9-03.
func storability(cfg *Config, c *keys.Classified, resp *Response, body []byte, respTime time.Time) storeDecision {
	d := storeDecision{cc: httpcc.ParseResponse(resp.Header)}
	d.lifetime, d.heuristic = httpcc.Lifetime(d.cc, resp.Header, resp.StatusCode, respTime, cfg.freshness())
	fail := func(reason string, responseDriven bool) storeDecision {
		// T-31: under unkeyed inputs no reason is response-driven, whichever
		// check happened to fail first.
		d.reason = reason
		d.responseDriven = responseDriven && !c.Authorized && !c.ReqCC.NoStore
		return d
	}
	h := resp.Header
	statusOK := slices.Contains(cfg.Storable.Statuses, resp.StatusCode)
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
	case c.Authorized && !d.cc.Public && !d.cc.SMaxAge.Set && !d.cc.MustRevalidate:
		// RFC 9111 §3.5, T-8. Request-driven: the credentials are unkeyed.
		return fail("authorization", false)
	case len(h["Set-Cookie"]) > 0 && !cfg.Storable.StripSetCookie:
		return fail("set-cookie", true) // T-8
	case len(h["Vary"]) > 0:
		// ponytail: every Vary response is refused until variant keying
		// lands (M7-01), so nothing is keyed on inputs it ignores.
		return fail("vary-unsupported", true)
	case !hasFreshness(d, h, resp.StatusCode):
		return fail("no-freshness", true) // T-6: never inferred from the path
	case int64(len(body))+headerBytes(h) > cfg.Storable.MaxObjectBytes:
		return fail("too-large", true)
	}
	d.ok = true
	return d
}

// hasFreshness is FR-STO-8. 302 and 307 need explicit freshness (D39).
func hasFreshness(d storeDecision, h http.Header, status int) bool {
	explicit := d.cc.SMaxAge.Set || d.cc.MaxAge.Set || len(h["Expires"]) > 0
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
