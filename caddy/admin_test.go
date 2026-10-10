package weircaddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// adminDo sends one request through the admin router the way Caddy's mux
// would and returns the recorded reply. Errors come back as caddy.APIError,
// as the real admin handler would render them.
func adminDo(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(), method, "http://localhost:2019"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	routes := adminRouter{}.Routes()
	if len(routes) != 1 || routes[0].Pattern != "/weir/" {
		t.Fatalf("routes = %+v", routes)
	}
	return w, routes[0].Handler.ServeHTTP(w, r)
}

// adminStatus runs adminDo and returns the HTTP status the admin layer would
// send.
func adminStatus(t *testing.T, method, path, body string) (int, *httptest.ResponseRecorder) {
	t.Helper()
	w, err := adminDo(t, method, path, body)
	if err != nil {
		apiErr, ok := errors.AsType[caddy.APIError](err)
		if !ok {
			t.Fatalf("%s %s: error %T %v, want caddy.APIError", method, path, err, err)
		}
		return apiErr.HTTPStatus, w
	}
	if w.Code == 0 {
		return http.StatusOK, w
	}
	return w.Code, w
}

func serveGet(t *testing.T, h *Handler, next caddyhttp.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com"+path, nil)
	if err := h.ServeHTTP(w, r, next); err != nil {
		t.Fatal(err)
	}
	return w
}

// FR-PRG-1, FR-PRG-2, T-26: a purge through the admin API reaches the engine
// that serves the site; the next request is stale-forwarded, not a hit.
func TestAdminPurgeChangesCacheStatus(t *testing.T) {
	h, _ := loadServe(t, "")
	var calls atomic.Int32
	next := respond(&calls, "body")
	serveGet(t, h, next, "/a")
	if cs := serveGet(t, h, next, "/a").Header().Get("Cache-Status"); !strings.Contains(cs, "hit") {
		t.Fatalf("second request Cache-Status = %q, want a hit", cs)
	}
	code, w := adminStatus(t, http.MethodPost, "/weir/"+h.Name+"/purge", `{"urls":["http://example.com/a"]}`)
	if code != http.StatusAccepted {
		t.Fatalf("purge status %d body %s", code, w.Body)
	}
	if cs := serveGet(t, h, next, "/a").Header().Get("Cache-Status"); !strings.Contains(cs, "fwd=stale") {
		t.Fatalf("after purge Cache-Status = %q, want fwd=stale", cs)
	}
}

// FR-PRG-8: an eager hard purge reports how many records it deleted.
func TestAdminEagerPurgeReportsScrubbed(t *testing.T) {
	h, _ := loadServe(t, "")
	var calls atomic.Int32
	next := respond(&calls, "body")
	for _, p := range []string{"/a", "/b"} {
		serveGet(t, h, next, p)
	}
	code, w := adminStatus(t, http.MethodPost, "/weir/"+h.Name+"/purge",
		`{"mode":"hard","eager":true,"urls":["http://example.com/a"]}`)
	if code != http.StatusAccepted {
		t.Fatalf("status %d body %s", code, w.Body)
	}
	var got struct {
		Scrubbed *int `json:"scrubbed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Scrubbed == nil || *got.Scrubbed != 1 {
		t.Fatalf("body %s (err %v), want scrubbed 1", w.Body, err)
	}
	// Without eager there is no count to report.
	code, w = adminStatus(t, http.MethodPost, "/weir/"+h.Name+"/purge", `{"all":true}`)
	if code != http.StatusAccepted || strings.Contains(w.Body.String(), "scrubbed") {
		t.Fatalf("non-eager purge: status %d body %s", code, w.Body)
	}
}

// FR-PRG-1: invalid purge input is a 400 and writes nothing; eager with a
// soft purge is invalid.
func TestAdminPurgeRejectsInvalidInput(t *testing.T) {
	h, _ := loadServe(t, "")
	path := "/weir/" + h.Name + "/purge"
	for name, body := range map[string]string{
		"empty purge":           `{}`,
		"eager with soft":       `{"eager":true,"all":true}`,
		"unknown mode":          `{"mode":"medium","all":true}`,
		"unknown field":         `{"all":true,"colour":"red"}`,
		"relative url":          `{"urls":["/a"]}`,
		"groups without origin": `{"groups":["g"]}`,
		"not json":              `all`,
		"trailing data":         `{"all":true}{"all":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, w := adminStatus(t, http.MethodPost, path, body); code != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", code, w.Body)
			}
		})
	}
}

// NFR-3, rule 5, 08 §7: the body, the URL list and the group list are bounded
// before any epoch is written.
func TestAdminPurgeBodyBounds(t *testing.T) {
	h, _ := loadServe(t, "")
	path := "/weir/" + h.Name + "/purge"
	urls := func(n int) string {
		b, _ := json.Marshal(map[string]any{"urls": repeat("http://example.com/x", n)})
		return string(b)
	}
	groups := func(n int) string {
		b, _ := json.Marshal(map[string]any{"origin": "http://example.com", "groups": repeat("g", n)})
		return string(b)
	}
	cases := []struct {
		name string
		body string
		want int
	}{
		{"1000 urls pass", urls(1000), http.StatusAccepted},
		{"1001 urls", urls(1001), http.StatusBadRequest},
		{"100 groups pass", groups(100), http.StatusAccepted},
		{"101 groups", groups(101), http.StatusBadRequest},
		{"body over 1 MiB", `{"all":true,"origin":"` + strings.Repeat("a", 1<<20) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, w := adminStatus(t, http.MethodPost, path, tc.body); code != tc.want {
				t.Fatalf("status %d (%.80s), want %d", code, w.Body, tc.want)
			}
		})
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// FR-MODE-1, FR-MODE-3, D33: the mode endpoint switches the incident mode;
// bypass sends every request to the origin and stores nothing.
func TestAdminModeSwitch(t *testing.T) {
	h, _ := loadServe(t, "")
	var calls atomic.Int32
	next := respond(&calls, "body")
	path := "/weir/" + h.Name + "/mode"
	if code, w := adminStatus(t, http.MethodPost, path, `{"mode":"bypass","ttl":"30m"}`); code != http.StatusOK {
		t.Fatalf("status %d body %s", code, w.Body)
	}
	serveGet(t, h, next, "/m")
	serveGet(t, h, next, "/m")
	if calls.Load() != 2 {
		t.Fatalf("origin calls under bypass = %d, want 2", calls.Load())
	}
	if code, w := adminStatus(t, http.MethodPost, path, `{"mode":"normal"}`); code != http.StatusOK {
		t.Fatalf("back to normal: status %d body %s", code, w.Body)
	}
	serveGet(t, h, next, "/n")
	serveGet(t, h, next, "/n")
	if calls.Load() != 3 {
		t.Fatalf("origin calls after normal = %d, want 3", calls.Load())
	}
	for name, body := range map[string]string{
		"unknown mode":      `{"mode":"panic","ttl":"1m"}`,
		"missing ttl":       `{"mode":"bypass"}`,
		"unparsable ttl":    `{"mode":"bypass","ttl":"soon"}`,
		"ttl over 24 hours": `{"mode":"bypass","ttl":"25h"}`,
		"negative ttl":      `{"mode":"bypass","ttl":"-1m"}`,
		"unknown field":     `{"mode":"bypass","ttl":"1m","x":1}`,
		"not json":          `bypass`,
	} {
		t.Run(name, func(t *testing.T) {
			if code, w := adminStatus(t, http.MethodPost, path, body); code != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", code, w.Body)
			}
		})
	}
}

// FR-OBS-1, 04 §9.2: stats is a JSON array with one entry per live engine.
func TestAdminStats(t *testing.T) {
	h, _ := loadServe(t, "")
	var calls atomic.Int32
	serveGet(t, h, respond(&calls, "body"), "/s")
	code, w := adminStatus(t, http.MethodGet, "/weir/"+h.Name+"/stats", "")
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var got []struct {
		Inflight     *int   `json:"inflight"`
		Queued       *int   `json:"queued"`
		BreakerState string `json:"breaker_state"`
		StoreBytes   *int64 `json:"store_bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", w.Body, err)
	}
	if len(got) != 1 || got[0].Inflight == nil || got[0].Queued == nil || got[0].BreakerState != "closed" ||
		got[0].StoreBytes == nil || *got[0].StoreBytes <= 0 {
		t.Fatalf("stats = %s", w.Body)
	}
}

// 08 §7: unknown names, unknown actions, wrong methods and odd paths.
func TestAdminUnknownNameIs404(t *testing.T) {
	h, _ := loadServe(t, "")
	for name, tc := range map[string]struct {
		method, path string
		want         int
	}{
		"unknown name stats":   {http.MethodGet, "/weir/nobody/stats", 404},
		"unknown name purge":   {http.MethodPost, "/weir/nobody/purge", 404},
		"unknown name mode":    {http.MethodPost, "/weir/nobody/mode", 404},
		"unknown action":       {http.MethodGet, "/weir/" + h.Name + "/flush", 404},
		"no action":            {http.MethodGet, "/weir/" + h.Name, 404},
		"extra segment":        {http.MethodGet, "/weir/" + h.Name + "/stats/x", 404},
		"empty name":           {http.MethodGet, "/weir//stats", 404},
		"dot name":             {http.MethodGet, "/weir/../stats", 404},
		"invalid name charset": {http.MethodGet, "/weir/a%20b/stats", 404},
		"GET on purge":         {http.MethodGet, "/weir/" + h.Name + "/purge", 405},
		"POST on stats":        {http.MethodPost, "/weir/" + h.Name + "/stats", 405},
		"PUT on mode":          {http.MethodPut, "/weir/" + h.Name + "/mode", 405},
	} {
		t.Run(name, func(t *testing.T) {
			if code, w := adminStatus(t, tc.method, tc.path, "{}"); code != tc.want {
				t.Fatalf("status %d body %s, want %d", code, w.Body, tc.want)
			}
		})
	}
}

// loadNamed provisions a handler with a fixed name and a store setting that
// differs between calls, so two loads of one name can overlap.
func loadNamed(t *testing.T, name, extra string) *Handler {
	t.Helper()
	h, err := load(t, fmt.Sprintf(`{"name":%q%s}`, name, extra))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// 08 §7: during a reload overlap both engines are registered; stats lists
// both, and once the old one cleans up only the new one is left.
func TestRegistryAcrossReloadOverlap(t *testing.T) {
	name := fmt.Sprintf("overlap-%d", serveSeq.Add(1))
	oldH := loadNamed(t, name, "")
	newH := loadNamed(t, name, "")
	if n := len(engineList(name)); n != 2 {
		t.Fatalf("live engines during overlap = %d, want 2", n)
	}
	_, w := adminStatus(t, http.MethodGet, "/weir/"+name+"/stats", "")
	var arr []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("stats during overlap = %s (err %v), want 2 entries", w.Body, err)
	}
	if err := oldH.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if got := engineList(name); len(got) != 1 || got[0].engine != newH.engine {
		t.Fatalf("after old cleanup: %d engines, new one registered = %v", len(got), len(got) == 1 && got[0].engine == newH.engine)
	}
	if err := newH.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if code, _ := adminStatus(t, http.MethodGet, "/weir/"+name+"/stats", ""); code != http.StatusNotFound {
		t.Fatalf("after all cleanups: status %d, want 404", code)
	}
}

// 08 §7: a failed load cleans up its own engine only; the serving engine
// stays reachable through the admin API.
func TestRegistryFailedLoadKeepsServingEngine(t *testing.T) {
	name := fmt.Sprintf("failed-%d", serveSeq.Add(1))
	serving := loadNamed(t, name, "")
	failing := loadNamed(t, name, "") // stands for the half-built engine of a failed load
	if err := failing.Cleanup(); err != nil {
		t.Fatal(err)
	}
	got := engineList(name)
	if len(got) != 1 || got[0].engine != serving.engine {
		t.Fatalf("serving engine not the only registered one: %d entries", len(got))
	}
	if code, w := adminStatus(t, http.MethodGet, "/weir/"+name+"/stats", ""); code != http.StatusOK {
		t.Fatalf("stats status %d body %s", code, w.Body)
	}
	// A second Cleanup (Caddy may call it twice) must not remove anything else.
	if err := failing.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if n := len(engineList(name)); n != 1 {
		t.Fatalf("engines after repeated cleanup = %d, want 1", n)
	}
}

// 08 §7: mode goes to every live engine of the name, so the old and new
// engine of an overlap agree.
func TestAdminModeAppliesToAllEngines(t *testing.T) {
	name := fmt.Sprintf("modes-%d", serveSeq.Add(1))
	a, b := loadNamed(t, name, ""), loadNamed(t, name, "")
	var calls atomic.Int32
	next := respond(&calls, "body")
	if code, w := adminStatus(t, http.MethodPost, "/weir/"+name+"/mode", `{"mode":"bypass","ttl":"10m"}`); code != http.StatusOK {
		t.Fatalf("status %d body %s", code, w.Body)
	}
	for _, h := range []*Handler{a, b} {
		serveGet(t, h, next, "/same")
		serveGet(t, h, next, "/same")
	}
	if calls.Load() != 4 {
		t.Fatalf("origin calls = %d, want 4 (bypass on both engines)", calls.Load())
	}
}

// 08 §7: purge goes through one live engine; an engine already closed is
// skipped, and when none is left the answer is 503.
func TestAdminClosedEngineIs503(t *testing.T) {
	name := fmt.Sprintf("closed-%d", serveSeq.Add(1))
	h := loadNamed(t, name, "")
	// Close the engine behind the registry's back, as Cleanup does before it
	// unregisters.
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if err := h.engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if code, w := adminStatus(t, http.MethodPost, "/weir/"+name+"/purge", `{"all":true}`); code != http.StatusServiceUnavailable {
		t.Fatalf("purge on closed engine: status %d body %s, want 503", code, w.Body)
	}
}
