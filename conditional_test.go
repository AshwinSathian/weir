package weir_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// recordingStore keeps every entry written, in order.
type recordingStore struct {
	store.Store
	mu   sync.Mutex
	sets []*store.Entry
}

func (s *recordingStore) Set(ctx context.Context, k store.Key, e *store.Entry) error {
	s.mu.Lock()
	s.sets = append(s.sets, e)
	s.mu.Unlock()
	return s.Store.Set(ctx, k, e)
}

func (s *recordingStore) responses() []*store.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*store.Entry
	for _, e := range s.sets {
		if e.Kind == store.KindResponse {
			out = append(out, e)
		}
	}
	return out
}

func newRecordingEngine(t *testing.T) (*weir.Engine, *recordingStore) {
	t.Helper()
	m, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	st := &recordingStore{Store: m}
	cfg := cacheCfg
	cfg.Store = st
	return newEngine(t, cfg), st
}

// respond builds an origin response with an empty or given body.
func respond(status int, h http.Header, body string) *weir.Response {
	return &weir.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

const lastMod = "Mon, 01 Jan 2024 00:00:00 GMT"

// FR-SRV-3, P4, FR-FRS-5: a stale entry is validated with its ETag and
// Last-Modified; the 304 updates the stored headers except Content-Length,
// restarts freshness and is stored as a new entry.
func TestRevalidation304Freshens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{
			"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}, "Last-Modified": {lastMod},
			"Content-Type": {"text/plain"},
		}, Body: []byte("v1")})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{
				"Cache-Control": {"max-age=120"}, "X-New": {"y"}, "Content-Length": {"999"},
			}, ""), nil
		}})
		resp, body := serve(t, e, getReq("/a"), o)

		reqs := o.Requests()
		v := reqs[len(reqs)-1].Header
		if v.Get("If-None-Match") != `"1"` || v.Get("If-Modified-Since") != lastMod {
			t.Fatalf("validation request conditionals: %v", v)
		}
		if body != "v1" || resp.StatusCode != http.StatusOK {
			t.Fatalf("got %d %q, want the stored 200 v1", resp.StatusCode, body)
		}
		h := resp.Header
		if h.Get("X-New") != "y" || h.Get("Cache-Control") != "max-age=120" || h.Get("Content-Type") != "text/plain" || h.Get("Content-Length") == "999" {
			t.Fatalf("freshened headers: %v", h)
		}
		if got, want := h.Get("Cache-Status"), "Weir; fwd=stale; fwd-status=304; stored"; got != want {
			t.Fatalf("Cache-Status %q, want %q", got, want)
		}

		time.Sleep(100 * time.Second) // fresh under the new max-age
		resp, body = serve(t, e, getReq("/a"), o)
		if !resp.Cache.Hit || body != "v1" || resp.Header.Get("X-New") != "y" {
			t.Fatalf("after freshen: hit=%v %q %v", resp.Cache.Hit, body, resp.Header)
		}
		if n := o.Calls("/a"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}

		ents := st.responses()
		if len(ents) != 2 || ents[0] == ents[1] {
			t.Fatalf("stored %d response entries, want 2 distinct", len(ents))
		}
		if old := ents[0]; old.Header.Get("Cache-Control") != "max-age=60" || old.Header.Get("X-New") != "" || old.Lifetime != time.Minute {
			t.Fatalf("freshen mutated the prior entry: %v lifetime %v", old.Header, old.Lifetime)
		}
		if nw := ents[1]; nw.Lifetime != 2*time.Minute || string(nw.Body) != "v1" || nw.Status != http.StatusOK {
			t.Fatalf("freshened entry: lifetime %v status %d body %q", nw.Lifetime, nw.Status, nw.Body)
		}
	})
}

// FR-SRV-3, RFC 9110 §15.4.5: a 304 cannot relabel the stored body. Its
// Content-Encoding and Content-Type are ignored; other fields still freshen.
func TestFreshenKeepsRepresentationMetadata(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{
			"Cache-Control": {"max-age=60"}, "Etag": {`W/"1"`},
			"Content-Type": {"text/plain"}, "Content-Encoding": {"gzip"},
		}, Body: []byte("v1")})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{
				"Cache-Control": {"max-age=120"}, "Content-Type": {"text/html"}, "Content-Encoding": {"br"},
			}, ""), nil
		}})
		resp, body := serve(t, e, getReq("/a"), o)
		h := resp.Header
		if body != "v1" || h.Get("Content-Type") != "text/plain" || h.Get("Content-Encoding") != "gzip" || h.Get("Cache-Control") != "max-age=120" {
			t.Fatalf("served after 304: %q %v", body, h)
		}
		ents := st.responses()
		if nw := ents[len(ents)-1]; nw.Header.Get("Content-Type") != "text/plain" || nw.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("stored after 304: %v", nw.Header)
		}
	})
}

// FR-FWD-7, FR-SRV-3: a 304's hop-by-hop fields and the fields its
// Connection names never reach the served or the freshened entry, while a
// Cache-Control it names still decides storage.
func TestFreshenDropsHopByHop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{
				"Cache-Control": {"max-age=120"}, "Connection": {"X-Conn"}, "X-Conn": {"1"}, "Keep-Alive": {"timeout=5"},
			}, ""), nil
		}})
		resp, _ := serve(t, e, getReq("/a"), o)
		ents := st.responses()
		for _, h := range []http.Header{resp.Header, ents[len(ents)-1].Header} {
			for _, name := range []string{"Connection", "X-Conn", "Keep-Alive"} {
				if _, ok := h[name]; ok {
					t.Fatalf("%s survived the 304: %v", name, h)
				}
			}
		}
		if len(ents) != 2 || resp.Header.Get("Cache-Control") != "max-age=120" {
			t.Fatalf("stored %d entries, served %v; want the freshened entry stored", len(ents), resp.Header)
		}

		time.Sleep(121 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{
				"Connection": {"Cache-Control"}, "Cache-Control": {"private, max-age=600"},
			}, ""), nil
		}})
		if resp, _ := serve(t, e, getReq("/a"), o); resp.Cache.Stored || len(st.responses()) != 2 {
			t.Fatalf("a 304 naming its private Cache-Control in Connection was stored: %+v", resp.Cache)
		}
	})
}

// FR-SRV-3: a 304 whose strong ETag differs from the stored one must not
// update the entry (RFC 9111 §4.3.4); Weir repeats the request without
// conditionals and stores the full response.
func TestStrongETagMismatchRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			h := http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"2"`}}
			if r.Header.Get("If-None-Match") != "" {
				return respond(http.StatusNotModified, h, ""), nil
			}
			return respond(http.StatusOK, h, "v2"), nil
		}})
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "v2" || resp.Cache.FwdStatus != http.StatusOK || !resp.Cache.Stored {
			t.Fatalf("got %q %+v, want the unconditional v2", body, resp.Cache)
		}
		reqs := o.Requests()
		if n := len(reqs); n != 3 || reqs[1].Header.Get("If-None-Match") != `"1"` || reqs[2].Header.Get("If-None-Match") != "" {
			t.Fatalf("origin saw %d requests, want the conditional then the unconditional one", n)
		}
		ents := st.responses()
		if len(ents) != 2 || string(ents[1].Body) != "v2" || ents[1].ETag != `"2"` {
			t.Fatalf("stored entries: %d, last %+v", len(ents), ents[len(ents)-1])
		}
		if _, body = serve(t, e, getReq("/a"), o); body != "v2" {
			t.Fatalf("next hit %q, want v2", body)
		}
	})
}

// FR-SRV-3, P5: a 304 answering a validation is freshened even when it
// carries a body over MaxObjectBytes; the body is discarded, not streamed.
func TestRevalidation304WithBody(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
		cfg := cacheCfg
		cfg.Storable.MaxObjectBytes = 256
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Status: http.StatusNotModified, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: []byte(strings.Repeat("x", 1024))})
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "v1" || resp.StatusCode != http.StatusOK || !resp.Cache.Stored {
			t.Fatalf("got %d %q %+v, want the freshened v1", resp.StatusCode, body, resp.Cache)
		}
	})
}

// FR-STO-5, T-8: an entry stored from an Authorization request keeps the
// shared-cache permission rule when a request without Authorization
// freshens it: a 304 that drops public leaves the entry unstored.
func TestFreshenKeepsAuthorizedRule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"public, max-age=60"}, "Etag": {`"1"`}}, Body: []byte("secret")})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		auth := getReq("/a")
		auth.Header.Set("Authorization", "Bearer x")
		if resp, _ := serve(t, e, auth, o); !resp.Cache.Stored {
			t.Fatalf("public response to an authorized request not stored: %+v", resp.Cache)
		}
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Status: http.StatusNotModified, Header: http.Header{"Cache-Control": {"max-age=60"}}})
		resp, body := serve(t, e, getReq("/a"), o)
		if body != "secret" || resp.Cache.Stored {
			t.Fatalf("freshened without public: body %q stored=%v, want served but not stored", body, resp.Cache.Stored)
		}
		if n := len(st.responses()); n != 1 {
			t.Fatalf("stored %d response entries, want 1", n)
		}
	})
}

// FR-SRV-3: a stale entry without validators is refetched unconditionally.
func TestStaleWithoutValidatorsRefetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v1"))
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		serve(t, e, getReq("/a"), o)
		reqs := o.Requests()
		if h := reqs[len(reqs)-1].Header; h.Get("If-None-Match") != "" || h.Get("If-Modified-Since") != "" {
			t.Fatalf("unexpected conditionals: %v", h)
		}
	})
}

// FR-SRV-2, RFC 9110 §13.2.2, §15.4.5: a hit on a stored 200 answers a
// failed client precondition with a 304 carrying only the listed fields.
func TestClientIfNoneMatch304(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stored := http.Header{
			"Cache-Control": {"max-age=600"}, "Etag": {`"1"`}, "Last-Modified": {lastMod},
			"Content-Location": {"/a.txt"}, "Expires": {"Thu, 01 Jan 2099 00:00:00 GMT"},
			"Content-Type": {"text/plain"}, "X-Other": {"z"},
		}
		noLM := http.Header{"Cache-Control": {"max-age=600"}}
		o := testorigin.NewChecked(t, 64, 16)
		o.Route("/a", testorigin.Behavior{Header: stored, Body: []byte("v1")})
		o.Route("/nolm", testorigin.Behavior{Header: noLM, Body: []byte("v1")})
		o.Route("/404", testorigin.Behavior{Status: http.StatusNotFound, Header: stored, Body: []byte("gone")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		start := time.Now()
		for _, p := range []string{"/a", "/nolm", "/404"} {
			serve(t, e, getReq(p), o)
		}
		time.Sleep(10 * time.Second)

		fmtT := func(t time.Time) string { return t.UTC().Format(http.TimeFormat) }
		for _, tc := range []struct {
			name, path string
			h          http.Header
			want       int
		}{
			{"matching strong tag", "/a", http.Header{"If-None-Match": {`"1"`}}, 304},
			{"weak comparison", "/a", http.Header{"If-None-Match": {`W/"1"`}}, 304},
			{"any of a list", "/a", http.Header{"If-None-Match": {`"0", "1"`}}, 304},
			{"star", "/a", http.Header{"If-None-Match": {"*"}}, 304},
			{"other tag", "/a", http.Header{"If-None-Match": {`"2"`}}, 200},
			{"If-None-Match wins over If-Modified-Since", "/a", http.Header{"If-None-Match": {`"2"`}, "If-Modified-Since": {lastMod}}, 200},
			{"malformed If-None-Match still suppresses If-Modified-Since", "/a", http.Header{"If-None-Match": {"abc"}, "If-Modified-Since": {lastMod}}, 200},
			{"not modified since", "/a", http.Header{"If-Modified-Since": {lastMod}}, 304},
			{"modified since", "/a", http.Header{"If-Modified-Since": {"Sun, 31 Dec 2023 00:00:00 GMT"}}, 200},
			{"no Last-Modified uses Date", "/nolm", http.Header{"If-Modified-Since": {fmtT(start)}}, 304},
			{"no Last-Modified, older than Date", "/nolm", http.Header{"If-Modified-Since": {fmtT(start.Add(-time.Hour))}}, 200},
			{"stored 404 is not evaluated", "/404", http.Header{"If-None-Match": {`"1"`}}, 404},
		} {
			// synctest forbids t.Run inside the bubble.
			func() {
				req := getReq(tc.path)
				for k, v := range tc.h {
					req.Header[k] = v
				}
				resp, body := serve(t, e, req, o)
				if resp.StatusCode != tc.want || !resp.Cache.Hit {
					t.Errorf("%s: status %d hit=%v, want %d from cache", tc.name, resp.StatusCode, resp.Cache.Hit, tc.want)
					return
				}
				if tc.want != 304 {
					return
				}
				if body != "" {
					t.Errorf("%s: 304 body %q", tc.name, body)
				}
				h := resp.Header
				for _, f := range []string{"Content-Type", "X-Other", "Content-Length"} {
					if _, ok := h[f]; ok {
						t.Errorf("%s: 304 carries %s: %v", tc.name, f, h)
					}
				}
				want := []string{"Date", "Age", "Cache-Status"}
				if tc.path == "/a" {
					want = append(want, "Cache-Control", "Content-Location", "Etag", "Expires")
				}
				for _, f := range want {
					if len(h.Values(f)) == 0 {
						t.Errorf("%s: 304 lacks %s: %v", tc.name, f, h)
					}
				}
			}()
		}
		if n := o.TotalCalls(); n != 3 {
			t.Fatalf("origin calls = %d, want 3", n)
		}
	})
}
