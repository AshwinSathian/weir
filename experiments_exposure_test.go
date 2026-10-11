package weir_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/AshwinSathian/weir"
)

func exposureConfig(consent bool) (weir.Config, *eventLog) {
	cfg := expConfig()
	log := &eventLog{}
	cfg.Observer = log
	if consent {
		cfg.Experiments.Consent = weir.ExperimentConsent{Cookie: "cmp", Value: "analytics=yes"}
	}
	return cfg, log
}

func (l *eventLog) exposures() []weir.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []weir.Event
	for _, ev := range l.evs {
		if ev.Kind == weir.EvExposure {
			out = append(out, ev)
		}
	}
	return out
}

// E1, 10 §4 step 7: one exposure when the assignment is created, and none on
// later visits, because the cookie that response set carries the signed
// exposed mark.
func TestExposureOncePerVisitor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		cfg, log := exposureConfig(false)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		first, _ := serve(t, e, expReq("/checkout", nil), o)
		cs := expCookieOf(first)
		if len(cs) != 1 || !strings.Contains(cs[0], "!.") {
			t.Fatalf("Set-Cookie = %q, want one cookie with the exposed mark", cs)
		}
		got := log.exposures()
		if len(got) != 1 || got[0].Experiment != "checkout-copy" || (got[0].Variant != "control" && got[0].Variant != "b") {
			t.Fatalf("exposures after first visit = %+v, want one for checkout-copy", got)
		}
		jar := http.Header{"Cookie": {cookiePair(cs[0])}}
		for range 3 {
			r, _ := serve(t, e, expReq("/checkout", jar), o)
			if c := expCookieOf(r); len(c) != 0 {
				t.Fatalf("returning visitor got Set-Cookie %q", c)
			}
		}
		if n := len(log.exposures()); n != 1 {
			t.Fatalf("exposures after 3 returning visits = %d, want 1", n)
		}
		log.checkVocabulary(t)
	})
}

// 10 §4 step 7: a signed assignment without the mark (an old cookie, or one
// minted before exposure tracking) gets its exposure on the next request,
// which is a cache hit here, and the mark is added to the cookie.
func TestExposureOnHit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		cfg, log := exposureConfig(false)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		unmarked := http.Header{"Cookie": {visitorMark(t, "seen-it", "b", false)}}
		if _, _ = serve(t, e, expReq("/checkout", unmarked), o); len(log.exposures()) != 1 {
			t.Fatalf("exposures after miss = %d, want 1", len(log.exposures()))
		}
		resp, _ := serve(t, e, expReq("/checkout", unmarked), o)
		if !resp.Cache.Hit {
			t.Fatalf("second request was not a hit: %+v", resp.Cache)
		}
		if n := len(log.exposures()); n != 2 {
			t.Fatalf("exposures = %d, want 2 (the cookie was not updated between the requests)", n)
		}
		cs := expCookieOf(resp)
		if len(cs) != 1 || !strings.Contains(cs[0], "checkout-copy:b!.") {
			t.Fatalf("Set-Cookie = %q, want the same variant with the mark", cs)
		}
		if ev := log.exposures()[1]; ev.Variant != "b" {
			t.Fatalf("exposure variant = %q, want b", ev.Variant)
		}
	})
}

// E6, E4: no consent means no exposure (and no cookie); a winner buckets
// nobody; Warm has no visitor.
func TestExposureNoneWithoutConsent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		cfg, log := exposureConfig(true)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, expReq("/checkout", nil), o)
		// Consent withdrawn: the cookie still holds an assignment, but the
		// visitor is served control and nothing is recorded.
		serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitorMark(t, "withdrew", "control", false)}}), o)
		if n := len(log.exposures()); n != 0 {
			t.Fatalf("exposures without consent = %d, want 0", n)
		}
		serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitorMark(t, "v1", "b", false) + "; cmp=analytics=yes"}}), o)
		if n := len(log.exposures()); n != 1 {
			t.Fatalf("exposures with consent = %d, want 1", n)
		}
	})

	t.Run("winner and warm record nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
			cfg, log := exposureConfig(false)
			cfg.Experiments.Experiments[0].Winner = "b"
			e := newEngine(t, cfg)
			defer closeEngine(t, e)
			serve(t, e, expReq("/checkout", nil), o)
			if n := len(log.exposures()); n != 0 {
				t.Fatalf("exposures for a winner = %d, want 0", n)
			}
		})
		synctest.Test(t, func(t *testing.T) {
			o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
			cfg, log := exposureConfig(false)
			e := newEngine(t, cfg)
			defer closeEngine(t, e)
			if _, err := e.Warm(t.Context(), slices.Values([]*weir.Request{expReq("/checkout", nil)}), o); err != nil {
				t.Fatalf("Warm: %v", err)
			}
			if n := len(log.exposures()); n != 0 {
				t.Fatalf("exposures from Warm = %d, want 0", n)
			}
		})
	})
}

// 10 §4 step 7: the event carries a SHA-256 prefix of the visitor id, never the id,
// and no request data (no partition, no reason).
func TestExposureVisitorIsHashPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := headerOrigin(t, http.Header{"Cache-Control": {"max-age=60"}})
		cfg, log := exposureConfig(false)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, expReq("/checkout", http.Header{"Cookie": {visitorMark(t, "visitor-abc", "b", false)}}), o)
		got := log.exposures()
		if len(got) != 1 {
			t.Fatalf("exposures = %d, want 1", len(got))
		}
		sum := sha256.Sum256([]byte("visitor-abc"))
		if want := hex.EncodeToString(sum[:8]); got[0].Visitor != want {
			t.Fatalf("Visitor = %q, want %q", got[0].Visitor, want)
		}
		ev := got[0]
		if ev.Partition != "" || ev.Reason != "" || ev.Status != 0 || ev.Info != (weir.CacheInfo{}) || ev.Time.IsZero() {
			t.Fatalf("event carries more than experiment, variant, visitor and time: %+v", ev)
		}
	})
}
