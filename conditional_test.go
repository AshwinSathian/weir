package weir_test

import (
	"context"
	"io"
	"net/http"
	"slices"
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
	mu       sync.Mutex
	sets     []*store.Entry
	down     *store.Key // Get of this key fails with ErrUnavailable
	downGets int        // how often it was read
}

func (s *recordingStore) Get(ctx context.Context, k store.Key) (*store.Entry, error) {
	s.mu.Lock()
	down := s.down != nil && *s.down == k
	if down {
		s.downGets++
	}
	s.mu.Unlock()
	if down {
		return nil, store.ErrUnavailable
	}
	return s.Store.Get(ctx, k)
}

// firstVariant returns the variant key in the first vary spec written.
func (s *recordingStore) firstVariant(t *testing.T) store.Key {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.sets {
		if e.Kind == store.KindVarySpec {
			return e.Variants[0].Key
		}
	}
	t.Fatal("no vary spec written")
	return store.Key{}
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
	return newRecordingEngineCfg(t, func(*weir.Config) {})
}

// newRecordingEngineCfg is newRecordingEngine with cacheCfg adjusted by set.
func newRecordingEngineCfg(t *testing.T, set func(*weir.Config)) (*weir.Engine, *recordingStore) {
	t.Helper()
	m, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	st := &recordingStore{Store: m}
	cfg := cacheCfg
	cfg.Store = st
	set(&cfg)
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

// INV-4, FR-SRV-3, FR-STO-3, FR-STO-6, FR-KEY-8; T-8: a 304 is merged into
// the stored response and judged like a full one, so a field that forbids
// storage is served to the client that got it and never to the next one.
func TestFreshenRefusesUnstorable304(t *testing.T) {
	tests := []struct {
		name string
		h    http.Header
	}{
		{"a 304 with Set-Cookie is not stored", http.Header{"Cache-Control": {"max-age=60"}, "Set-Cookie": {"sid=victim"}}},
		{"a private 304 is not stored", http.Header{"Cache-Control": {"private, max-age=60"}}},
		{"a no-store 304 is not stored", http.Header{"Cache-Control": {"no-store"}}},
		{"a 304 with Vary star is not stored", http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"*"}}},
		{"a 304 with Vary Cookie is not stored", http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Cookie"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
				e, st := newRecordingEngine(t)
				defer closeEngine(t, e)

				serve(t, e, getReq("/a"), o)
				time.Sleep(61 * time.Second)
				o.Default(testorigin.Behavior{Status: http.StatusNotModified, Header: tt.h})
				resp, body := serve(t, e, getReq("/a"), o)
				if body != "v1" || resp.Cache.Stored {
					t.Fatalf("body %q stored=%v, want served but not stored", body, resp.Cache.Stored)
				}
				for name, want := range tt.h { // FR-STO-6: its own client still gets every field
					if !slices.Equal(resp.Header[name], want) {
						t.Fatalf("%s served as %q, want %q", name, resp.Header[name], want)
					}
				}
				if n := len(st.responses()); n != 1 {
					t.Fatalf("stored %d response entries, want only the first", n)
				}

				// No Cache-Control on this 304, so the served one can only
				// come from the first entry, unchanged (P4).
				o.Default(testorigin.Behavior{Status: http.StatusNotModified})
				next, body := serve(t, e, getReq("/a"), o)
				if body != "v1" || len(next.Header["Set-Cookie"]) > 0 || len(next.Header["Vary"]) > 0 ||
					next.Header.Get("Cache-Control") != "max-age=60" {
					t.Fatalf("next client got body %q, header %v; want none of the refused 304's fields", body, next.Header)
				}
			})
		})
	}
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

// T-8, 05 §6: the codec refuses header names that are not canonical, so
// every entry the engine stores must already be canonical, whatever
// spelling a custom Origin used. Otherwise a remote store would fail Set.
func TestStoredEntriesEncode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(200, http.Header{"cache-control": {"max-age=60"}, "eTag": {`"text/a"`}, "x-odd": {"1"}, "vary": {"accept"}}, "v"), nil
		}})
		e, st := newRecordingEngine(t)
		defer closeEngine(t, e)

		serve(t, e, getReq("/e"), o)
		if resp, _ := serve(t, e, getReq("/e"), o); !resp.Cache.Hit {
			t.Fatalf("second request: %+v, want a hit", resp.Cache)
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if len(st.sets) < 2 {
			t.Fatalf("%d records written, want the variant and its spec", len(st.sets))
		}
		for _, ent := range st.sets {
			if _, err := store.Encode(ent); err != nil {
				t.Errorf("kind %v: %v", ent.Kind, err)
			}
		}
	})
}

// FR-STO-13, FR-SRV-3, RFC 9110 §6.6.1: a 304 without Date still leaves the
// freshened response, and the hit after it, with one Date, set at receipt.
func TestRevalidation304WithoutDateStamps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{"Cache-Control": {"max-age=60"}}, ""), nil
		}})
		want := time.Now().UTC().Format(http.TimeFormat)
		resp, _ := serve(t, e, getReq("/a"), o)
		if got := resp.Header.Get("Date"); got != want {
			t.Fatalf("revalidated Date = %q, want %q", got, want)
		}
		time.Sleep(5 * time.Second)
		resp, _ = serve(t, e, getReq("/a"), o)
		if !resp.Cache.Hit || resp.Header.Get("Date") != want {
			t.Fatalf("hit = %v Date = %q, want %q", resp.Cache.Hit, resp.Header.Get("Date"), want)
		}
	})
}

// FR-SRV-2, FR-SRV-3, INV-1, T-8: client preconditions are evaluated on every
// GET or HEAD response built from a stored or just-stored entry, not only on
// a hit. The forward carries the stored validators and never the client's.
func TestClientConditionalAfterRevalidation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hdr := http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}, "Last-Modified": {lastMod}, "Vary": {"Accept"}}
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: hdr, Body: []byte("v1")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Func: func(*weir.Request) (*weir.Response, error) {
			return respond(http.StatusNotModified, http.Header{"Cache-Control": {"max-age=60"}}, ""), nil
		}})

		with := func(method string, h http.Header) *weir.Request {
			r := getReq("/a")
			r.Method = method
			for k, v := range h {
				r.Header[k] = v
			}
			return r
		}
		resp, body := serve(t, e, with("GET", http.Header{"If-None-Match": {`"1"`}}), o)
		if resp.StatusCode != http.StatusNotModified || body != "" || resp.Header.Get("Age") == "" || resp.Header.Get("Cache-Status") == "" {
			t.Fatalf("matching If-None-Match after 304: %d %q %v", resp.StatusCode, body, resp.Header)
		}
		if resp.Header.Get("Content-Type") != "" || resp.Header.Get("Etag") != `"1"` {
			t.Fatalf("304 header set: %v", resp.Header)
		}
		last := o.Requests()[o.TotalCalls()-1].Header
		if last.Get("If-None-Match") != `"1"` || last.Get("If-Modified-Since") != lastMod {
			t.Fatalf("forward carries %v, want the stored validators", last)
		}

		time.Sleep(61 * time.Second)
		resp, body = serve(t, e, with("GET", http.Header{"If-None-Match": {`"9"`}}), o)
		if resp.StatusCode != http.StatusOK || body != "v1" {
			t.Fatalf("non-matching client: %d %q, want 200 v1", resp.StatusCode, body)
		}
		if got := o.Requests()[o.TotalCalls()-1].Header.Get("If-None-Match"); got != `"1"` {
			t.Fatalf("forward If-None-Match %q, want the stored tag", got)
		}

		time.Sleep(61 * time.Second)
		resp, _ = serve(t, e, with("HEAD", http.Header{"If-Modified-Since": {lastMod}}), o)
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("HEAD after 304: %d, want 304", resp.StatusCode)
		}
	})
}

// FR-SRV-2, FR-SRV-3: after a strong-ETag mismatch the retry's response is
// the final entry; the client is judged against it, not the discarded 304.
func TestClientConditionalUsesFinalEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"1"`}}, Body: []byte("v1")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)
		serve(t, e, getReq("/a"), o)
		time.Sleep(61 * time.Second)
		// The discarded 304 names "2", the retry's final entry "3".
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("If-None-Match") != "" {
				return respond(http.StatusNotModified, http.Header{"Etag": {`"2"`}}, ""), nil
			}
			return respond(http.StatusOK, http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"3"`}}, "v3"), nil
		}})
		r := getReq("/a")
		r.Header["If-None-Match"] = []string{`"2"`}
		resp, body := serve(t, e, r, o)
		if resp.StatusCode != http.StatusOK || body != "v3" {
			t.Fatalf("client holding the discarded tag: %d %q, want 200 v3", resp.StatusCode, body)
		}
		time.Sleep(61 * time.Second)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Etag": {`"3"`}}, Body: []byte("v3")})
		r = getReq("/a")
		r.Header["If-None-Match"] = []string{`"3"`}
		if resp, _ = serve(t, e, r, o); resp.StatusCode != http.StatusNotModified {
			t.Fatalf("client holding the final tag: %d, want 304", resp.StatusCode)
		}
	})
}

// FR-SRV-2, FR-KEY-7, T-8: each Vary variant is judged against its own
// stored entry, and an Authorization request whose public response was
// stored gets the same treatment.
func TestClientConditionalVariantsAndAuthorization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		tag := func(r *weir.Request) string { return `"` + r.Header.Get("X-Tenant") + `"` }
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("If-None-Match") == tag(r) {
				return respond(http.StatusNotModified, http.Header{"Cache-Control": {"public, max-age=60"}}, ""), nil
			}
			return respond(http.StatusOK, http.Header{"Cache-Control": {"public, max-age=60"}, "Etag": {tag(r)}, "Vary": {"X-Tenant"}}, "for "+tag(r)), nil
		}})
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"X-Tenant"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		req := func(accept, inm string, auth bool) *weir.Request {
			r := getReq("/v")
			r.Header.Set("X-Tenant", accept)
			if inm != "" {
				r.Header["If-None-Match"] = []string{inm}
			}
			if auth {
				r.Header.Set("Authorization", "Bearer x")
			}
			return r
		}
		serve(t, e, req("ta", "", false), o)
		serve(t, e, req("tb", "", true), o)
		time.Sleep(61 * time.Second)
		for _, tc := range []struct {
			name string
			r    *weir.Request
			want int
		}{
			{"own variant tag", req("ta", `"ta"`, false), 304},
			{"other variant tag", req("ta", `"tb"`, false), 200},
			{"authorized, own tag", req("tb", `"tb"`, true), 304},
			{"authorized, other tag", req("tb", `"ta"`, true), 200},
		} {
			time.Sleep(61 * time.Second) // every case revalidates
			if resp, _ := serve(t, e, tc.r, o); resp.StatusCode != tc.want {
				t.Errorf("%s: status %d (%+v) %v want %d", tc.name, resp.StatusCode, resp.Cache, resp.Header, tc.want)
			}
		}
	})
}

// FR-SRV-2, FR-COA-1: a cold miss and each flight follower answer by their
// own conditionals.
func TestClientConditionalColdMissAndFollowers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		b := cacheable("v1")
		b.Header["Etag"] = []string{`"1"`}
		b.Delay = 100 * time.Millisecond
		o.Default(b)
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		match, other, plain := getReq("/c"), getReq("/c"), getReq("/c")
		match.Header["If-None-Match"] = []string{`"1"`}
		other.Header["If-None-Match"] = []string{`"2"`}
		chs := []<-chan served{serveAsync(t.Context(), e, match, o), serveAsync(t.Context(), e, other, o), serveAsync(t.Context(), e, plain, o)}
		want := []int{304, 200, 200}
		collapsed := 0
		for i, ch := range chs {
			s := <-ch
			if s.err != nil || s.resp.StatusCode != want[i] {
				t.Fatalf("client %d: %v status %d, want %d", i, s.err, s.resp.StatusCode, want[i])
			}
			if (want[i] == 304) != (s.body == "") {
				t.Fatalf("client %d: body %q", i, s.body)
			}
			if s.resp.Cache.Collapsed {
				collapsed++
			}
		}
		if n := o.TotalCalls(); n != 1 || collapsed != 2 {
			t.Fatalf("origin calls %d, collapsed %d; want 1 and 2", n, collapsed)
		}
	})
}
