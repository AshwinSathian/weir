package weir_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/experiments"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

var expKey = []byte("0123456789abcdef0123456789abcdef-exp-key")

func expConfig() weir.Config {
	c := cacheCfg
	c.Experiments = weir.ExperimentsConfig{
		SigningKeys: [][]byte{expKey},
		Experiments: []weir.ExperimentConfig{{
			Name: "checkout-copy", Salt: "s1", Path: "/checkout*",
			Variants: []weir.VariantConfig{{Name: "control", Weight: 50}, {Name: "b", Weight: 50}},
		}},
	}
	return c
}

// visitor returns a Cookie header value holding a signed assignment whose
// exposure is already recorded, so serving it rewrites nothing.
func visitor(t *testing.T, id, variant string) string { return visitorMark(t, id, variant, true) }

// visitorMark is visitor with the exposed mark optional.
func visitorMark(t *testing.T, id, variant string, exposed bool) string {
	t.Helper()
	c := experiments.Cookie{VisitorID: id}
	if variant != "" {
		c.Set("checkout-copy", variant)
		if exposed {
			c.MarkExposed("checkout-copy")
		}
	}
	v, ok := c.Encode(expKey)
	if !ok {
		t.Fatal("encode refused")
	}
	return "weir_v=" + v
}

func expReq(path string, h http.Header) *weir.Request {
	r := getReq(path)
	for k, v := range h {
		r.Header[k] = v
	}
	return r
}

// echoOrigin builds an origin whose body is the variant header it saw.
func echoOrigin(t *testing.T) *testorigin.Origin {
	o := testorigin.NewChecked(t, 64, 16)
	o.Default(testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
		return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}},
			Body: io.NopCloser(strings.NewReader("variant=" + strings.Join(r.Header["Weir-Variant"], ",")))}, nil
	}})
	return o
}

// T-35, FR-KEY-2: two variants are two entries and never mix.
func TestVariantIsKeyDimension(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		a := http.Header{"Cookie": {visitor(t, "v-a", "control")}}
		b := http.Header{"Cookie": {visitor(t, "v-b", "b")}}
		for range 2 {
			if _, body := serve(t, e, expReq("/checkout", a), o); body != "variant=checkout-copy=control" {
				t.Fatalf("a got %q", body)
			}
			if _, body := serve(t, e, expReq("/checkout", b), o); body != "variant=checkout-copy=b" {
				t.Fatalf("b got %q", body)
			}
		}
		if n := o.Calls("/checkout"); n != 2 {
			t.Fatalf("origin calls = %d, want one per variant", n)
		}
	})
}

// INV-1, T-35: different visitors in one variant share an entry, and the
// origin never sees the visitor cookie or a client-chosen variant header.
func TestForwardedEqualsKeyed(t *testing.T) {
	for _, mode := range []weir.ForwardMode{weir.ForwardStrict, weir.ForwardAll} {
		synctest.Test(t, func(t *testing.T) {
			o := echoOrigin(t)
			cfg := expConfig()
			cfg.Forward.Mode = mode
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			for _, id := range []string{"one", "two", "three"} {
				h := http.Header{"Cookie": {visitor(t, id, "b") + "; theme=dark"}, "Weir_Variant": {"checkout-copy=evil"}}
				serve(t, e, expReq("/checkout", h), o)
			}
			if n := o.Calls("/checkout"); n != 1 {
				t.Fatalf("mode %v: origin calls = %d, want 1", mode, n)
			}
			for _, r := range o.Requests() {
				for k, v := range r.Header {
					if strings.EqualFold(strings.ReplaceAll(k, "_", "-"), "weir-variant") && (k != "Weir-Variant" || len(v) != 1 || v[0] != "checkout-copy=b") {
						t.Fatalf("mode %v: origin saw %s: %q", mode, k, v)
					}
				}
			}
		})
	}
}

// T-35, INV-1: the experiment cookie is removed in every forwarding mode;
// other cookies are kept where the mode forwards them.
func TestExperimentCookieNeverForwarded(t *testing.T) {
	tests := []struct {
		name   string
		edit   func(*weir.Config)
		cookie string // what the origin should see
	}{
		{"strict with a keyed cookie", func(c *weir.Config) { c.Key.Cookies = []string{"theme"} }, "theme=dark"},
		{"strict without keyed cookies", func(*weir.Config) {}, ""},
		{"forward all", func(c *weir.Config) { c.Forward.Mode = weir.ForwardAll }, "theme=dark"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				o := echoOrigin(t)
				cfg := expConfig()
				tt.edit(&cfg)
				e := newEngine(t, cfg)
				defer closeEngine(t, e)

				serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b") + "; theme=dark"}}), o)
				rs := o.Requests()
				if len(rs) != 1 {
					t.Fatalf("origin saw %d requests", len(rs))
				}
				if got := strings.Join(rs[0].Header["Cookie"], "|"); got != tt.cookie {
					t.Fatalf("origin Cookie = %q, want %q", got, tt.cookie)
				}
			})
		})
	}
}

// E6: without the consent cookie a visitor gets control, even with a valid assignment.
func TestConsentAbsentGetsControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		cfg := expConfig()
		cfg.Experiments.Consent = weir.ExperimentConsent{Cookie: "cmp_consent", Value: "analytics=yes"}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		_, body := serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b")}}), o)
		if body != "variant=checkout-copy=control" {
			t.Fatalf("no consent: %q", body)
		}
		_, body = serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b") + "; cmp_consent=ads=no&analytics=yes"}}), o)
		if body != "variant=checkout-copy=b" {
			t.Fatalf("consent: %q", body)
		}
	})
}

// E4: a winner serves every visitor and keeps its key dimension.
func TestWinnerOverridesAssignment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		cfg := expConfig()
		cfg.Experiments.Experiments[0].Winner = "b"
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, h := range []http.Header{
			{"Cookie": {visitor(t, "v", "control")}}, nil, {"Cookie": {"weir_v=forged"}},
		} {
			if _, body := serve(t, e, expReq("/checkout", h), o); body != "variant=checkout-copy=b" {
				t.Fatalf("winner: %q", body)
			}
		}
		if n := o.Calls("/checkout"); n != 1 {
			t.Fatalf("origin calls = %d, want 1", n)
		}
	})
}

// E2, T-35: forged or foreign cookies are a new visitor, not a chosen variant.
func TestForgedCookieCannotChooseVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		bad := experiments.Cookie{VisitorID: "x"}
		bad.Set("checkout-copy", "b")
		forged, _ := bad.Encode([]byte("a-different-key-of-at-least-32-bytes!!"))
		seen := map[string]bool{}
		for _, v := range []string{forged, "v1.x.checkout-copy:b.AAAA", "garbage"} {
			_, body := serve(t, e, expReq("/checkout", http.Header{"Cookie": {"weir_v=" + v}}), o)
			seen[body] = true
		}
		// The assignment comes from the visitor id the engine minted, never from the forged value.
		for body := range seen {
			if body != "variant=checkout-copy=control" && body != "variant=checkout-copy=b" {
				t.Fatalf("unexpected body %q", body)
			}
		}
	})
}

// E1, E8: a request outside every matcher has no dimension and no header.
func TestUnmatchedPathHasNoVariant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		e := newEngine(t, expConfig())
		defer closeEngine(t, e)

		_, body := serve(t, e, expReq("/other", http.Header{"Weir-Variant": {"checkout-copy=b"}}), o)
		if body != "variant=" {
			t.Fatalf("got %q", body)
		}
	})
}

// E6, E4, P2: Warm stores the control variant, or the winner, whatever Cookie
// the warm request carries and with or without a consent gate; other variants
// fill on demand.
func TestWarmFetchesControlVariant(t *testing.T) {
	for _, gated := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			o := echoOrigin(t)
			cfg := expConfig()
			if gated {
				cfg.Experiments.Consent = weir.ExperimentConsent{Cookie: "cmp_consent", Value: "analytics=yes"}
			}
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			warm := expReq("/checkout", http.Header{"Cookie": {visitor(t, "v", "b") + "; cmp_consent=analytics=yes"}})
			st, err := e.Warm(t.Context(), func(yield func(*weir.Request) bool) { yield(warm) }, o)
			if err != nil || st.Fetched != 1 {
				t.Fatalf("Warm = %+v, %v", st, err)
			}
			ctl := http.Header{"Cookie": {visitor(t, "v", "control") + "; cmp_consent=analytics=yes"}}
			resp, body := serve(t, e, expReq("/checkout", ctl), o)
			if !resp.Cache.Hit || body != "variant=checkout-copy=control" {
				t.Fatalf("gated=%v: control after warm: hit=%v %q", gated, resp.Cache.Hit, body)
			}
			if n := o.Calls("/checkout"); n != 1 {
				t.Fatalf("gated=%v: origin calls = %d", gated, n)
			}
			b := http.Header{"Cookie": {visitor(t, "v", "b") + "; cmp_consent=analytics=yes"}}
			if resp, _ := serve(t, e, expReq("/checkout", b), o); resp.Cache.Hit {
				t.Fatalf("gated=%v: variant b was warmed", gated)
			}
		})
	}
}

// E4: a winner is warmed and served to everyone.
func TestWinnerWarmed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := echoOrigin(t)
		cfg := expConfig()
		cfg.Experiments.Experiments[0].Winner = "b"
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		warm := expReq("/checkout", nil)
		if st, err := e.Warm(t.Context(), func(yield func(*weir.Request) bool) { yield(warm) }, o); err != nil || st.Fetched != 1 {
			t.Fatalf("Warm = %+v, %v", st, err)
		}
		resp, body := serve(t, e, expReq("/checkout", nil), o)
		if !resp.Cache.Hit || body != "variant=checkout-copy=b" {
			t.Fatalf("hit=%v %q", resp.Cache.Hit, body)
		}
	})
}
