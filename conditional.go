package weir

import (
	"maps"
	"net/http"
	"strings"

	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// hasValidators reports whether ent can be validated with a conditional
// request (FR-SRV-3).
func hasValidators(ent *store.Entry) bool {
	return ent.ETag != "" || !ent.LastModified.IsZero()
}

// withValidators returns a copy of req that validates prior (FR-SRV-3).
// req is not modified. These are the Weir validators INV-1 allows unkeyed.
func withValidators(req *Request, prior *store.Entry) *Request {
	r := *req
	r.Header = req.Header.Clone()
	if r.Header == nil {
		r.Header = http.Header{}
	}
	if prior.ETag != "" {
		r.Header["If-None-Match"] = []string{prior.ETag}
	}
	if !prior.LastModified.IsZero() {
		r.Header["If-Modified-Since"] = []string{prior.LastModified.UTC().Format(http.TimeFormat)}
	}
	return &r
}

// strongETagMismatch reports whether a 304 names a strong ETag other than
// the stored one, which forbids updating the entry (RFC 9111 §4.3.4).
func strongETagMismatch(h http.Header, stored string) bool {
	tag := h.Get("Etag")
	return tag != "" && !strings.HasPrefix(tag, "W/") && tag != stored
}

// freshened merges the fields of a 304 into prior's stored response
// (FR-SRV-3, RFC 9111 §4.3.4): every 304 field replaces the stored one,
// except Content-Length, which describes the empty 304 body, and
// Content-Encoding and Content-Type, which only the stored body can vouch
// for (RFC 9110 §15.4.5). buildEntry then drops what storage excludes and
// recomputes freshness. prior is not modified (P4).
func freshened(prior *store.Entry, notModified http.Header) *Response {
	h := maps.Clone(prior.Header)
	if h == nil {
		h = http.Header{}
	}
	delete(h, "Date") // a 304 without Date dates the entry now (FR-STO-13), like a full response
	for k, v := range notModified {
		switch k {
		case "Content-Length", "Content-Encoding", "Content-Type":
		default:
			h[k] = v
		}
	}
	return &Response{StatusCode: prior.Status, Header: h, Body: http.NoBody}
}

// clientNotModified evaluates the client's preconditions against a stored
// 200 (FR-SRV-2, RFC 9110 §13.2.2): If-None-Match with weak comparison,
// else If-Modified-Since against Last-Modified or, without it, Date.
func clientNotModified(cc *keys.ClientConditionals, ent *store.Entry) bool {
	if ent.Status != http.StatusOK {
		return false
	}
	if cc.IfNoneMatch != nil {
		stored := opaqueTag(ent.ETag)
		for _, t := range cc.IfNoneMatch {
			if t == "*" || (stored != "" && opaqueTag(t) == stored) {
				return true
			}
		}
		return false
	}
	if cc.IfModifiedSince.IsZero() {
		return false
	}
	lm := ent.LastModified
	if lm.IsZero() {
		lm = ent.Date
	}
	return !lm.After(cc.IfModifiedSince)
}

// opaqueTag strips a weak prefix for weak comparison. A stored ETag that is
// not a quoted string yields "" and never matches.
func opaqueTag(t string) string {
	t = strings.TrimPrefix(t, "W/")
	if len(t) < 2 || t[0] != '"' || t[len(t)-1] != '"' {
		return ""
	}
	return t
}

// notModifiedHeader picks the stored fields a 304 carries (RFC 9110
// §15.4.5). The value slices stay shared; they are clipped (04 §6.10).
func notModifiedHeader(stored http.Header) http.Header {
	h := make(http.Header, 8)
	for _, k := range [...]string{"Cache-Control", "Content-Location", "Date", "Etag", "Expires", "Vary"} {
		if v, ok := stored[k]; ok {
			h[k] = v
		}
	}
	return h
}
