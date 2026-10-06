package weir

import (
	"net/http"
	"slices"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// buildEntry builds the response entry for a fetch sent at reqTime and
// received at respTime, from the decision storability returned (04 §4.2,
// §6.7). resp.Header is not modified: the triggering client still gets
// every field, Set-Cookie included (FR-STO-6).
func buildEntry(cfg *Config, c *keys.Classified, resp *Response, body []byte, reqTime, respTime time.Time, d storeDecision) *store.Entry {
	h := storedHeader(resp.Header, cfg.Storable.StripSetCookie)
	date, ok := httpcc.ParseDate(h.Get("Date"))
	if !ok { // FR-STO-13
		date = respTime
		h["Date"] = []string{respTime.UTC().Format(http.TimeFormat)}
	}
	lm, _ := httpcc.ParseDate(h.Get("Last-Modified"))
	etag := h.Get("Etag")

	f := &cfg.Freshness
	lifetime := httpcc.Jitter(d.lifetime, f.Jitter, f.JitterMinLifetime, cfg.Rand()) // FR-FRS-5
	swr, sie := httpcc.StaleWindows(d.cc, cfg.freshness())
	cia := httpcc.CorrectedInitialAge(resp.Header.Get("Age"), reqTime, respTime)

	e := &store.Entry{
		Kind:                store.KindResponse,
		StoredAt:            respTime,
		Status:              resp.StatusCode,
		Header:              h,
		Body:                body,
		RequestTime:         reqTime,
		ResponseTime:        respTime,
		Date:                date,
		CorrectedInitialAge: cia,
		Lifetime:            lifetime,
		SWR:                 swr,
		SIE:                 sie,
		Flags:               entryFlags(d, c.Authorized),
		ETag:                etag,
		LastModified:        lm,
		FetchDuration:       max(respTime.Sub(reqTime), 0),
		VaryNames:           d.varyNames,
		Tags:                []store.Tag{store.TagGlobal(), c.OriginTag, c.URITag},
		Owner:               c.OriginTag,
	}
	for _, g := range d.groups { // at most Limits.MaxGroups (FR-STO-10)
		e.Tags = append(e.Tags, keys.TagGroup(c.Origin, g)) // FR-PRG-6, T-25: scoped to the origin
	}
	// Retention (04 §4.2). Both terms of lifetime-cia are non-negative, so
	// it cannot overflow; the rest is added to the time one term at a time,
	// so a huge Keep cannot wrap a Duration sum into the past.
	exp := respTime.Add(lifetime - cia).Add(max(swr, sie))
	if etag != "" || !lm.IsZero() {
		exp = exp.Add(f.Keep)
	}
	e.Expires = later(exp, respTime.Add(time.Second))
	return e
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func entryFlags(d storeDecision, authorized bool) store.Flags {
	var fl store.Flags
	for _, x := range [...]struct {
		on bool
		f  store.Flags
	}{
		{d.cc.MustRevalidate, store.FlagMustRevalidate},
		{d.cc.ProxyRevalidate, store.FlagProxyRevalidate},
		{d.cc.NoCache, store.FlagNoCache},
		{d.cc.SMaxAge.Set, store.FlagSMaxAge},
		{d.heuristic, store.FlagHeuristic},
		{d.cc.Public, store.FlagPublic},
		{authorized, store.FlagFromAuthorized},
	} {
		if x.on {
			fl |= x.f
		}
	}
	return fl
}

// storedHeader copies h without the fields FR-STO-11 excludes. Every value
// slice is clipped (len == cap) so an append on a served shallow clone
// reallocates instead of writing into the stored array (P4, 04 §6.10).
func storedHeader(h http.Header, stripSetCookie bool) http.Header {
	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	keys.DropHopByHop(out, h["Connection"])
	for _, name := range [...]string{"Proxy-Authenticate", "Proxy-Authentication-Info", "Age"} {
		delete(out, name)
	}
	if stripSetCookie {
		delete(out, "Set-Cookie")
	}
	for name, vals := range out {
		out[name] = slices.Clip(vals)
	}
	return out
}
