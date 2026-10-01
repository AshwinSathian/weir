package weir

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// invalidate marks the target URI of an unsafe request, and the same-origin
// URIs its 2xx or 3xx response names in Location and Content-Location,
// invalid (FR-INV-1, FR-INV-3, RFC 9111 §4.4). The epoch is written after
// the response arrived, so every entry requested before it is covered
// (FR-PRG-7). A failed write is ignored: the client's response does not
// depend on it, and the memory store fails only once closed.
func (e *Engine) invalidate(ctx context.Context, c *keys.Classified, resp *Response) {
	ctx = context.WithoutCancel(ctx) // the response is already on its way to the client
	tags := []store.Tag{c.URITag}
	base, err := url.ParseRequestURI(c.Forwarded.Path) // Path is escaped; relative references resolve against it
	if err != nil {
		base = &url.URL{Path: "/"}
	}
	base.Scheme, base.Host = c.Forwarded.Scheme, c.Forwarded.Host
	for _, name := range [...]string{"Location", "Content-Location"} {
		if t, ok := e.sameOriginTag(c, base, resp.Header.Get(name)); ok {
			tags = append(tags, t)
		}
	}
	ep := store.Epoch{At: time.Now(), Mode: store.EpochInvalid}
	emit(e.cfg.Observer, Event{Kind: EvPurge, Time: ep.At, Partition: c.Partition, Reason: "invalid"})
	for _, t := range tags {
		_ = e.sg.setEpoch(ctx, t, ep)
	}
}

// sameOriginTag resolves ref against base and returns its URI tag when it
// names the request's origin. The URI is validated and rewritten exactly
// like a request, so it tags what a GET for it would store; invalid or
// cross-origin values are ignored.
func (e *Engine) sameOriginTag(c *keys.Classified, base *url.URL, ref string) (store.Tag, bool) {
	if ref == "" {
		return store.Tag{}, false
	}
	r, err := url.Parse(ref)
	if err != nil {
		return store.Tag{}, false
	}
	u := base.ResolveReference(r)
	kc, err := keys.Classify(&keys.Request{Method: http.MethodGet, Scheme: u.Scheme, Host: u.Host,
		Path: u.EscapedPath(), RawQuery: u.RawQuery}, &e.kcfg)
	if err != nil || kc.Origin != c.Origin {
		return store.Tag{}, false
	}
	return kc.URITag, true
}
