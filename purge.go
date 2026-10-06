package weir

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// Purge makes the entries p names stale (PurgeSoft) or unreachable
// (PurgeHard) by writing one epoch per tag (FR-PRG-1, 04 §7). Its cost is
// the number of tags, not the number of matching entries, which are judged
// at their next lookup (FR-PRG-4). All of p is validated before the first
// write; invalid input returns an error matching ErrInvalidRequest and
// purges nothing. A store error stops the call and is returned: epochs
// already written stay, EvPurge is still emitted for them, and repeating
// the call is safe. The memory store rounds soft epochs up to the next
// whole second (05 E-7), so a soft purge by URL or group can take up to 1 s
// to show; hard purges and All apply at once.
//
// Eager with PurgeSoft is invalid input. No store scrubs yet (M15), so
// Eager with PurgeHard writes its epochs and returns ErrEagerUnsupported
// (FR-PRG-8).
func (e *Engine) Purge(ctx context.Context, p Purge) error {
	if e.closed.Load() {
		return ErrClosed
	}
	mode, reason := store.EpochSoft, "soft"
	switch p.Mode {
	case PurgeSoft:
	case PurgeHard:
		mode, reason = store.EpochHard, "hard"
	default:
		return &RequestError{Reason: "purge-mode"}
	}
	if p.Eager && p.Mode != PurgeHard {
		return &RequestError{Reason: "purge-eager"} // FR-PRG-8
	}
	// Sized by the operator's own call, not by request input (P5).
	tags := make([]store.Tag, 0, 1+len(p.URLs)+len(p.Groups))
	if p.All {
		tags = append(tags, keys.TagGlobal()) // FR-PRG-5
	}
	for i, u := range p.URLs {
		kc, err := e.classifyURL(u, false)
		if err != nil {
			return fmt.Errorf("%w: purge url %d", err, i) // the index, never the URL's text
		}
		tags = append(tags, kc.URITag)
	}
	if len(p.Groups) > 0 || p.Origin != "" { // an Origin nothing uses is still checked
		kc, err := e.classifyURL(p.Origin, true)
		if err != nil {
			return &RequestError{Reason: "purge-origin"}
		}
		for _, g := range p.Groups {
			tags = append(tags, keys.TagGroup(kc.Origin, g)) // FR-PRG-6
		}
	}
	if len(tags) == 0 {
		return nil
	}
	ep := store.Epoch{At: time.Now(), Mode: mode}
	for i, t := range tags {
		if err := e.sg.setEpoch(ctx, t, ep); err != nil {
			if e.closed.Load() { // Close won the race and closed the store
				return ErrClosed
			}
			if i > 0 { // 04 §9.2: epochs were written, so the purge is reported
				emit(e.cfg.Observer, Event{Kind: EvPurge, Time: ep.At, Reason: reason})
			}
			return fmt.Errorf("weir: purge: %w", err)
		}
	}
	emit(e.cfg.Observer, Event{Kind: EvPurge, Time: ep.At, Reason: reason})
	if p.Eager {
		return ErrEagerUnsupported // FR-PRG-8: the epochs are written, nothing was deleted
	}
	return nil
}

// classifyURL classifies a GET for the absolute URL raw, so its tags and
// origin are the ones a request for it gets (FR-PRG-1). The URL is cut by
// hand: net/url would re-escape path bytes a request keeps as sent, and the
// purge would then miss the entry. Userinfo and fragments are not stripped:
// "@" in the host and "#" anywhere fail validation, while "@" in a path or
// query is kept, as in a request. With originOnly, raw must be exactly
// scheme://host[:port].
func (e *Engine) classifyURL(raw string, originOnly bool) (keys.Classified, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return keys.Classified{}, &RequestError{Reason: "purge-url"}
	}
	host, path, query := rest, "/", ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		if originOnly {
			return keys.Classified{}, &RequestError{Reason: "purge-url"}
		}
		host = rest[:i]
		p, q, _ := strings.Cut(rest[i:], "?")
		if query = q; p != "" {
			path = p
		}
	}
	// Schemes are case-insensitive (RFC 9110 §4.2.3); Validate takes the lowercase form.
	kc, err := keys.Classify(&keys.Request{Method: http.MethodGet, Scheme: strings.ToLower(scheme), Host: host,
		Path: path, RawQuery: query}, &e.kcfg)
	if err != nil {
		reason := "purge-url"
		if re, ok := errors.AsType[*keys.RequestError](err); ok {
			reason = re.Reason
		}
		return keys.Classified{}, &RequestError{Reason: reason}
	}
	return kc, nil
}
