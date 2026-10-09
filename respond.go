package weir

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// fromEntry serves ent at now (04 §6.10), or a 304 when the client's
// preconditions fail (FR-SRV-2). The header map is a shallow clone: stored
// value slices are clipped, so a caller's Add reallocates and Set or Del
// only touch the clone (P4).
func (e *Engine) fromEntry(c *keys.Classified, ent *store.Entry, now time.Time, ci CacheInfo) *Response {
	age := []string{strconv.FormatInt(int64(httpcc.CurrentAge(ent, now)/time.Second), 10)} // FR-FRS-7
	if clientNotModified(&c.ClientCond, ent) {
		h := notModifiedHeader(ent.Header)
		h["Age"] = age
		return e.finish(&Response{StatusCode: http.StatusNotModified, Header: h, Body: http.NoBody}, ci)
	}
	h := maps.Clone(ent.Header)
	if h == nil {
		h = http.Header{}
	}
	h["Age"] = age
	data, status := ent.Body, ent.Status
	// FR-RNG-1..3, T-37. HEAD ignores Range; If-Range is judged first.
	if c.Range && !c.Head && ent.Status == http.StatusOK && !acceptRangesNone(ent.Header) && (!c.HasIfRange || httpcc.IfRangeApplies(c.IfRange, ent)) {
		start, end, kind := httpcc.ParseRange(c.RangeValue, int64(len(ent.Body)))
		switch kind {
		case httpcc.RangeOK:
			data, status = ent.Body[start:end+1], http.StatusPartialContent
			h["Content-Range"] = []string{"bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10) + "/" + strconv.Itoa(len(ent.Body))}
			h["Content-Length"] = []string{strconv.Itoa(len(data))}
		case httpcc.RangeUnsatisfiable:
			h = notModifiedHeader(ent.Header)
			h["Age"] = age
			h["Content-Range"] = []string{"bytes */" + strconv.Itoa(len(ent.Body))}
			return e.finish(&Response{StatusCode: http.StatusRequestedRangeNotSatisfiable, Header: h, Body: http.NoBody}, ci)
		}
	}
	var body io.ReadCloser = http.NoBody
	if !c.Head && len(data) > 0 {
		body = io.NopCloser(bytes.NewReader(data))
	}
	return e.finish(&Response{StatusCode: status, Header: h, Body: body}, ci)
}

// finish sets ci and appends the Cache-Status member after any the origin
// sent (FR-SRV-9). Header value slices may be shared with a stored entry,
// so the member goes into a new slice.
func (e *Engine) finish(r *Response, ci CacheInfo) *Response {
	r.Cache = ci
	// FR-TCC-4: clients never see Weir-Cache-Control, but the stored copy
	// keeps it. The header map may belong to an entry (P4), so clone first.
	if _, ok := r.Header["Weir-Cache-Control"]; ok {
		r.Header = maps.Clone(r.Header)
		delete(r.Header, "Weir-Cache-Control")
	}
	if !e.cfg.NoCacheStatus {
		old := r.Header["Cache-Status"]
		r.Header["Cache-Status"] = append(old[:len(old):len(old)], cacheStatus(e.cfg.CacheStatus, ci))
	}
	return r
}

// acceptRangesNone reports whether the origin disclaimed range support on the
// stored response (RFC 9110 §14.3). Weir then ignores Range rather than
// contradict the header it serves with the 206 (RFC 9110 §14.2 allows it).
func acceptRangesNone(h http.Header) bool {
	for _, v := range h["Accept-Ranges"] {
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			return true
		}
	}
	return false
}
