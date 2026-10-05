package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/memory"
)

// varyBody answers with Vary: field and the field's forwarded value as the
// body, so a test can tell which variant it got.
func varyBody(field string, delay time.Duration) testorigin.Behavior {
	return testorigin.Behavior{Delay: delay, Func: func(r *weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: http.StatusOK,
			Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {field}},
			Body:   io.NopCloser(strings.NewReader(r.Header.Get(field)))}, nil
	}}
}

func varyReq(path, field, value string) *weir.Request {
	r := getReq(path)
	if value != "" {
		r.Header.Set(field, value)
	}
	return r
}

// FR-KEY-7, FR-KEY-9, T-15, T6.7: in VaryAuto a header named in Vary is
// folded into the variant key, read from the forwarded request. Two values
// give two variants; each is then a hit that returns its own body.
func TestVaryUnconfiguredHeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0))
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"X-Custom"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, v := range []string{"a", "b"} {
			if _, body := serve(t, e, varyReq("/v", "X-Custom", v), o); body != v {
				t.Fatalf("miss %s: body %q", v, body)
			}
		}
		for _, v := range []string{"a", "b"} {
			resp, body := serve(t, e, varyReq("/v", "X-Custom", v), o)
			if body != v || !resp.Cache.Hit {
				t.Fatalf("second %s: body %q hit=%v", v, body, resp.Cache.Hit)
			}
		}
		// FR-KEY-11: an absent field only matches absent.
		resp, body := serve(t, e, varyReq("/v", "X-Custom", ""), o)
		if body != "" || resp.Cache.Hit || resp.Cache.Fwd != weir.FwdVaryMiss {
			t.Fatalf("absent: body %q hit=%v fwd=%v", body, resp.Cache.Hit, resp.Cache.Fwd)
		}
		if n := o.Calls("/v"); n != 3 {
			t.Fatalf("origin calls = %d, want 3", n)
		}
	})
	// FR-KEY-9: VaryStrict stores nothing for a name Key.VaryAllow does
	// not list, so every request reaches the origin and gets its own answer.
	t.Run("strict without allow stores nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := testorigin.NewChecked(t, 64, 16)
			o.Default(varyBody("X-Custom", 0))
			var ev eventCounter
			cfg := cacheCfg
			cfg.Observer = &ev
			cfg.Forward.Allow = []string{"X-Custom"}
			cfg.Key.Vary = weir.VaryStrict
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			for range 2 {
				if resp, body := serve(t, e, varyReq("/v", "X-Custom", "a"), o); resp.Cache.Hit || body != "a" {
					t.Fatalf("hit=%v body %q, want a forwarded answer", resp.Cache.Hit, body)
				}
			}
			if n := ev.count(weir.EvNotStored.String() + "/vary-strict"); n != 2 || o.Calls("/v") != 2 {
				t.Fatalf("EvNotStored{vary-strict} = %d, origin calls = %d; want 2 and 2: %v", n, o.Calls("/v"), ev.n)
			}
		})
	})
}

// FR-KEY-9, T-8: a Vary naming Cookie, Authorization or Proxy-Authorization
// prevents storage unless Key.VaryAllow lists the name.
func TestVarySensitiveNotStored(t *testing.T) {
	for _, name := range []string{"Cookie", "authorization", "Accept, Proxy-Authorization"} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := testorigin.NewChecked(t, 64, 16)
				o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {name}}})
				var ev eventCounter
				cfg := cacheCfg
				cfg.Observer = &ev
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, getReq("/s"), o)
				if resp, _ := serve(t, e, getReq("/s"), o); resp.Cache.Hit {
					t.Fatal("a response varying on a sensitive field was served from the cache")
				}
				if n := ev.count(weir.EvNotStored.String() + "/vary-sensitive"); n != 2 || o.Calls("/s") != 2 {
					t.Fatalf("EvNotStored{vary-sensitive} = %d, origin calls = %d; want 2 and 2: %v", n, o.Calls("/s"), ev.n)
				}
			})
		})
	}
}

// FR-KEY-10, NFR-3, T-15: with MaxVariants live variants, a response for a
// new variant is answered but not stored, and the stored ones stay hits.
func TestVaryOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0))
		var ev eventCounter
		cfg := cacheCfg
		cfg.Observer = &ev
		cfg.Forward.Allow = []string{"X-Custom"}
		cfg.Key.MaxVariants = 2
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i, v := range []string{"a", "b", "c", "c"} {
			resp, body := serve(t, e, varyReq("/o", "X-Custom", v), o)
			if resp.Cache.Hit || body != v {
				t.Fatalf("%s: hit=%v body %q, want its own forwarded answer", v, resp.Cache.Hit, body)
			}
			// The overflow is an event, not a Cache-Status detail: the
			// member only loses "stored".
			want := "Weir; fwd=vary-miss; fwd-status=200"
			if i < 2 {
				want += "; stored"
			}
			if i == 0 {
				want = "Weir; fwd=uri-miss; fwd-status=200; stored"
			}
			if got := resp.Header.Get("Cache-Status"); got != want {
				t.Fatalf("%s: Cache-Status %q, want %q", v, got, want)
			}
		}
		if n := ev.count(weir.EvVaryOverflow.String() + "/"); n != 2 || o.Calls("/o") != 4 {
			t.Fatalf("EvVaryOverflow = %d, origin calls = %d; want 2 and 4: %v", n, o.Calls("/o"), ev.n)
		}
		for _, v := range []string{"a", "b"} {
			if resp, body := serve(t, e, varyReq("/o", "X-Custom", v), o); !resp.Cache.Hit || body != v {
				t.Fatalf("%s after overflow: hit=%v body %q", v, resp.Cache.Hit, body)
			}
		}
	})
}

// FR-KEY-10, D37: a ref past its Expires, or one whose record the store no
// longer has (evicted or deleted), frees its slot at the next spec update.
func TestVaryReclaimsDeadSlots(t *testing.T) {
	// The variant "short" passes its retention an hour before the others.
	origin := func(t *testing.T) *testorigin.Origin {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			v, cc := r.Header.Get("X-Custom"), "max-age=3600"
			if v == "short" {
				cc = "max-age=1"
			}
			return &weir.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Cache-Control": {cc}, "Vary": {"X-Custom"}},
				Body:   io.NopCloser(strings.NewReader(v))}, nil
		}})
		return o
	}
	// stored reports whether v was stored: its second request is a hit.
	stored := func(t *testing.T, e *weir.Engine, o weir.Origin, v string) bool {
		t.Helper()
		serve(t, e, varyReq("/r", "X-Custom", v), o)
		resp, body := serve(t, e, varyReq("/r", "X-Custom", v), o)
		if body != v {
			t.Fatalf("%s: body %q", v, body)
		}
		return resp.Cache.Hit
	}

	t.Run("expired variant", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			// A lazy store still returns short's record after its
			// Expires, so only the ref's Expires can free the slot.
			m, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			cfg := cacheCfg
			cfg.Store = &lazyStore{Store: m, m: map[store.Key]*store.Entry{}}
			cfg.Forward.Allow = []string{"X-Custom"}
			cfg.Key.MaxVariants = 2
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			if !stored(t, e, o, "short") || !stored(t, e, o, "b") || stored(t, e, o, "c") {
				t.Fatal("setup: want short and b stored, c over the cap")
			}
			time.Sleep(time.Minute) // past short's retention, inside b's
			if !stored(t, e, o, "c") {
				t.Fatal("c not stored after the short variant expired")
			}
			if resp, _ := serve(t, e, varyReq("/r", "X-Custom", "b"), o); !resp.Cache.Hit {
				t.Fatal("b lost its slot")
			}
		})
	})

	t.Run("record gone from the store", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			e, st := newRecordingEngineCfg(t, func(c *weir.Config) {
				c.Forward.Allow = []string{"X-Custom"}
				c.Key.MaxVariants = 2
			})
			defer closeEngine(t, e)

			if !stored(t, e, o, "a") || !stored(t, e, o, "b") || stored(t, e, o, "c") {
				t.Fatal("setup: want a and b stored, c over the cap")
			}
			// The first spec written lists only a's variant key.
			if err := st.Delete(t.Context(), st.firstVariant(t)); err != nil {
				t.Fatal(err)
			}
			if !stored(t, e, o, "c") {
				t.Fatal("c not stored though a's record is gone")
			}
			if resp, _ := serve(t, e, varyReq("/r", "X-Custom", "b"), o); !resp.Cache.Hit {
				t.Fatal("b lost its slot")
			}
			if stored(t, e, o, "a") {
				t.Fatal("a stored past the cap: b and c hold both slots")
			}
		})
	})

	// 04 §14: below the cap no slot is needed, so a spec write reads no
	// other variant.
	t.Run("no reads below the cap", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			e, st := newRecordingEngineCfg(t, func(c *weir.Config) {
				c.Forward.Allow = []string{"X-Custom"}
				c.Key.MaxVariants = 3
			})
			defer closeEngine(t, e)

			serve(t, e, varyReq("/r", "X-Custom", "a"), o)
			a := st.firstVariant(t)
			st.mu.Lock()
			st.down = &a
			st.mu.Unlock()
			serve(t, e, varyReq("/r", "X-Custom", "b"), o)
			serve(t, e, varyReq("/r", "X-Custom", "c"), o)
			st.mu.Lock()
			n := st.downGets
			st.mu.Unlock()
			if n != 0 {
				t.Fatalf("%d reads of a's variant while the spec was below the cap", n)
			}
		})
	})

	// T-15: only a definite ErrNotFound frees a slot. A store that fails
	// the read keeps the ref, or a flaky store would lift the cap.
	t.Run("store error keeps the slot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := origin(t)
			var ev eventCounter
			e, st := newRecordingEngineCfg(t, func(c *weir.Config) {
				c.Observer = &ev
				c.Forward.Allow = []string{"X-Custom"}
				c.Key.MaxVariants = 2
			})
			defer closeEngine(t, e)

			if !stored(t, e, o, "a") || !stored(t, e, o, "b") {
				t.Fatal("setup: want a and b stored")
			}
			a := st.firstVariant(t)
			st.mu.Lock()
			st.down = &a
			st.mu.Unlock()
			if stored(t, e, o, "c") {
				t.Fatal("c stored though a's read failed without ErrNotFound")
			}
			if n := ev.count(weir.EvVaryOverflow.String() + "/"); n != 2 {
				t.Fatalf("EvVaryOverflow = %d, want 2: %v", n, ev.n)
			}
		})
	})
}

// FR-KEY-11, T-15: values are keyed exactly as forwarded. Two lines a and
// b reach an origin that reads only the first line as "a", so a stored
// answer to them must not serve a request sending the single line "a,b".
func TestVaryKeysExactLines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0)) // the body is Header.Get: the first line
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"X-Custom"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		two := getReq("/v")
		two.Header["X-Custom"] = []string{"a", "b"}
		if _, body := serve(t, e, two, o); body != "a" {
			t.Fatalf("two lines: body %q", body)
		}
		resp, body := serve(t, e, varyReq("/v", "X-Custom", "a,b"), o)
		if resp.Cache.Hit || body != "a,b" {
			t.Fatalf("one line a,b: hit=%v body %q, want its own answer", resp.Cache.Hit, body)
		}
		if resp, _ := serve(t, e, varyReq("/v", "X-Custom", "a,b"), o); !resp.Cache.Hit {
			t.Fatalf("identical lines missed: %+v", resp.Cache)
		}
	})
}

// FR-KEY-8, FR-STO-7: Vary: * prevents storage.
func TestVaryStar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"Accept-Language, *"}}})
		var ev eventCounter
		cfg := cacheCfg
		cfg.Observer = &ev
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/s"), o)
		if resp, _ := serve(t, e, getReq("/s"), o); resp.Cache.Hit {
			t.Fatal("Vary: * response was served from the cache")
		}
		if n := ev.count(weir.EvNotStored.String() + "/vary-star"); n == 0 {
			t.Fatalf("no EvNotStored{vary-star}: %v", ev.n)
		}
	})
}

// FR-KEY-10: a Vary listing more than MaxVaryHeaders names is not stored.
func TestVaryTooManyNames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"A, B", "C"}}})
		var ev eventCounter
		cfg := cacheCfg
		cfg.Observer = &ev
		cfg.Key.MaxVaryHeaders = 2
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/m"), o)
		if n := ev.count(weir.EvNotStored.String() + "/vary-too-many"); n != 1 {
			t.Fatalf("EvNotStored{vary-too-many} = %d: %v", n, ev.n)
		}
	})
}

// FR-COA-5, FR-KEY-7: followers of a flight whose response varies on a
// field they differ in re-enter and coalesce again on their variant key:
// 300 requests over 3 languages cost 1 + 2 origin calls.
func TestVaryFollowersRecoalesce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("Accept-Language", 100*time.Millisecond))
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		langs := []string{"en", "fr", "de"}
		chs := make([]<-chan served, 300)
		for i := range chs {
			chs[i] = serveAsync(t.Context(), e, varyReq("/l", "Accept-Language", langs[i%3]), o)
		}
		for i, ch := range chs {
			s := <-ch
			if s.err != nil || s.body != langs[i%3] {
				t.Fatalf("request %d (%s): body %q err %v", i, langs[i%3], s.body, s.err)
			}
		}
		if n := o.Calls("/l"); n != 3 {
			t.Fatalf("origin calls = %d, want 1 + 2", n)
		}
	})
}

// FR-WRM-2, FR-KEY-7: a warm request that joins a flight storing another
// variant fetches its own instead of counting as skipped.
func TestWarmFollowerOfOtherVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		gate := make(chan struct{})
		b := varyBody("Accept-Language", 0)
		b.Gate = gate
		o.Default(b)
		cfg := cacheCfg
		cfg.Forward.Allow = []string{"Accept-Language"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		fg := serveAsync(t.Context(), e, varyReq("/w", "Accept-Language", "en"), o)
		synctest.Wait()
		warmed := make(chan weir.WarmStats, 1)
		go func() {
			st, _ := e.Warm(t.Context(), func(yield func(*weir.Request) bool) {
				yield(varyReq("/w", "Accept-Language", "fr"))
			}, o)
			warmed <- st
		}()
		synctest.Wait()
		close(gate)
		<-fg
		if st := <-warmed; st != (weir.WarmStats{Fetched: 1}) || o.Calls("/w") != 2 {
			t.Fatalf("Warm = %+v with %d origin calls; want 1 fetched, 2 calls", st, o.Calls("/w"))
		}
		if resp, body := serve(t, e, varyReq("/w", "Accept-Language", "fr"), o); !resp.Cache.Hit || body != "fr" {
			t.Fatalf("fr after warm: hit=%v body %q", resp.Cache.Hit, body)
		}
	})
}

// FR-NEG-1, FR-KEY-7: a negative entry for one variant lands under its
// variant key, so the vary spec and the other variants stay reachable.
// Accept-Encoding forwards only its bucket, so the request stays keyed
// (FR-NEG-4).
func TestVaryNegativeStaysInVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		ok := varyBody("Accept-Encoding", 0)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			if r.Header.Get("Accept-Encoding") == "identity" {
				return &weir.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
			}
			return ok.Func(r)
		}})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		serve(t, e, varyReq("/n", "Accept-Encoding", "gzip"), o)
		serve(t, e, varyReq("/n", "Accept-Encoding", ""), o)
		if resp, _ := serve(t, e, varyReq("/n", "Accept-Encoding", ""), o); resp.Cache.Detail != "negative" {
			t.Fatalf("identity: %+v, want a negative hit", resp.Cache)
		}
		if resp, body := serve(t, e, varyReq("/n", "Accept-Encoding", "gzip"), o); !resp.Cache.Hit || body != "gzip" {
			t.Fatalf("gzip: hit=%v body %q, want the stored variant", resp.Cache.Hit, body)
		}
		if n := o.Calls("/n"); n != 2 {
			t.Fatalf("origin calls = %d, want 2", n)
		}
	})
}

// INV-1, FR-KEY-7, T-15: a request header key in non-canonical case
// (possible for direct library callers) reaches the origin under
// ForwardAll, so the variant key must see it too; otherwise the origin's
// answer to it is stored as the "absent" variant and served to everyone.
func TestVaryNonCanonicalRequestKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
			var v string
			for k, vs := range r.Header { // an origin matching names case-insensitively
				if strings.EqualFold(k, "X-Custom") {
					v += strings.Join(vs, ",")
				}
			}
			return &weir.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Cache-Control": {"max-age=60"}, "Vary": {"X-Custom"}},
				Body:   io.NopCloser(strings.NewReader(v))}, nil
		}})
		cfg := cacheCfg
		cfg.Forward.Mode = weir.ForwardAll
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		evil := getReq("/c")
		evil.Header["x-custom"] = []string{"evil"}
		serve(t, e, evil, o)
		if _, body := serve(t, e, getReq("/c"), o); body != "" {
			t.Fatalf("a request without X-Custom got %q", body)
		}
	})
}
