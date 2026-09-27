package weir

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/AshwinSathian/weir/internal/httpcc"
	"github.com/AshwinSathian/weir/internal/keys"
	"github.com/AshwinSathian/weir/store"
)

// fromEntry serves ent at now (04 §6.10). The header map is a shallow
// clone: stored value slices are clipped, so a caller's Add reallocates and
// Set or Del only touch the clone (P4).
func (e *Engine) fromEntry(c *keys.Classified, ent *store.Entry, now time.Time, ci CacheInfo) *Response {
	h := maps.Clone(ent.Header)
	if h == nil {
		h = http.Header{}
	}
	h["Age"] = []string{strconv.FormatInt(int64(httpcc.CurrentAge(ent, now)/time.Second), 10)} // FR-FRS-7
	var body io.ReadCloser = http.NoBody
	if !c.Head && len(ent.Body) > 0 {
		body = io.NopCloser(bytes.NewReader(ent.Body))
	}
	return e.finish(&Response{StatusCode: ent.Status, Header: h, Body: body}, ci)
}

// finish sets ci and appends the Cache-Status member after any the origin
// sent (FR-SRV-9). Header value slices may be shared with a stored entry,
// so the member goes into a new slice.
func (e *Engine) finish(r *Response, ci CacheInfo) *Response {
	r.Cache = ci
	if !e.cfg.NoCacheStatus {
		old := r.Header["Cache-Status"]
		r.Header["Cache-Status"] = append(old[:len(old):len(old)], cacheStatus(e.cfg.CacheStatus, ci))
	}
	return r
}
