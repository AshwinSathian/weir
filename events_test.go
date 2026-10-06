package weir_test

import (
	"fmt"
	"math"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

// eventLog records every event; Observe runs on engine goroutines.
type eventLog struct {
	mu  sync.Mutex
	evs []weir.Event
}

func (l *eventLog) Observe(ev weir.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evs = append(l.evs, ev)
}

// find returns the first event of kind with reason; an empty reason matches
// any.
func (l *eventLog) find(kind weir.EventKind, reason string) (weir.Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ev := range l.evs {
		if ev.Kind == kind && (reason == "" || ev.Reason == reason) {
			return ev, true
		}
	}
	return weir.Event{}, false
}

// count returns how many events of kind were recorded.
func (l *eventLog) count(kind weir.EventKind) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, ev := range l.evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// vocabulary is the 04 §9.2 reason column. EvKeyRejected is absent: its
// reasons are RequestError.Reason values.
var vocabulary = map[weir.EventKind][]string{
	weir.EvRequest:         {"hit", "stale", "miss", "revalidated", "pass", "bypass", "negative", "error"},
	weir.EvFetchStart:      {"foreground", "background", "warm", "pass"},
	weir.EvFetchEnd:        {"foreground", "background", "warm", "pass"},
	weir.EvCoalesceJoin:    {""},
	weir.EvCoalesceTimeout: {"stale", "direct"},
	weir.EvShed:            {"queue-full", "queue-timeout", "background"},
	weir.EvStaleServed:     {"swr", "sie", "shed", "circuit-open", "coalesce-timeout"},
	weir.EvRefreshDropped:  {"no-slot", "circuit-open", "closed"},
	weir.EvBreakerState:    {"closed", "open", "half-open"},
	weir.EvStoreError:      {"get", "set", "epoch", "set-epoch"},
	weir.EvStoreBreaker:    {"open", "closed"},
	weir.EvNotStored: {"method", "status", "no-store", "private", "authorization", "set-cookie", "vary-star", "vary-sensitive",
		"vary-strict", "vary-too-many", "no-freshness", "too-large", "incomplete", "groups"},
	weir.EvVaryOverflow:    {""},
	weir.EvNegativeServed:  {""},
	weir.EvPurge:           {"soft", "hard", "invalid", "group", "group-invalid"},
	weir.EvMissRateAnomaly: {"flag", "throttle"},
	weir.EvEvict:           {"small", "main", "expired"},
	weir.EvMode:            {"normal", "stale-on-error", "bypass"},
}

// checkVocabulary fails for a recorded reason outside the catalog, so an
// emit site cannot lose its reason unnoticed.
func (l *eventLog) checkVocabulary(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ev := range l.evs {
		if words, ok := vocabulary[ev.Kind]; ok && !slices.Contains(words, ev.Reason) {
			t.Errorf("%v event has reason %q, outside the 04 §9.2 vocabulary", ev.Kind, ev.Reason)
		}
	}
}

// kindSet is the kinds the scenarios of one test made an observer receive.
type kindSet map[weir.EventKind]bool

// want fails unless l holds an event of kind for each reason, and returns
// the event with the last one.
func (s kindSet) want(t *testing.T, l *eventLog, kind weir.EventKind, reasons ...string) weir.Event {
	t.Helper()
	if len(reasons) == 0 {
		reasons = []string{""}
	}
	var ev weir.Event
	for _, r := range reasons {
		var ok bool
		if ev, ok = l.find(kind, r); !ok {
			t.Errorf("no %v event with reason %q", kind, r)
			continue
		}
		s[kind] = true
	}
	return ev
}

// FR-OBS-1, 04 §9.2: a scenario per EventKind makes the observer receive it,
// with the reasons of its vocabulary this card added or changed.
func TestEveryEventKindEmitted(t *testing.T) {
	seen := kindSet{}
	scenario := func(name string, cfg weir.Config, run func(t *testing.T, cfg weir.Config, l *eventLog)) {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l := &eventLog{}
				cfg.Freshness.NoJitter = true
				cfg.Observer = l
				run(t, cfg, l)
				l.checkVocabulary(t)
			})
		})
	}

	scenario("request outcomes and fetches", weir.Config{Storable: weir.StorableConfig{MaxObjectBytes: 4096}},
		func(t *testing.T, cfg weir.Config, l *eventLog) {
			const delay = 50 * time.Millisecond
			o := testorigin.NewChecked(t, 64, 16)
			a := cacheable("a")
			a.Delay = delay
			o.Route("/a", a)
			o.Route("/w", cacheable("w"))
			o.Route("/big", cacheable(strings.Repeat("x", 8192)))
			o.Route("/down", ccBehavior(http.StatusServiceUnavailable, "", "down"))
			cut := cacheable("truncated")
			cut.Truncate = 3
			o.Route("/cut", cut)
			o.Route("/swr", ccBehavior(http.StatusOK, "max-age=1, stale-while-revalidate=600", "s"))
			o.Route("/etag", testorigin.Behavior{Func: func(r *weir.Request) (*weir.Response, error) {
				if r.Header.Get("If-None-Match") != "" {
					return &weir.Response{StatusCode: http.StatusNotModified, Header: http.Header{"Cache-Control": {"max-age=1"}}}, nil
				}
				return &weir.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Cache-Control": {"max-age=1"}, "Etag": {`"v"`}},
					Body:   http.NoBody}, nil
			}})
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			serve(t, e, getReq("/a"), o)
			miss := seen.want(t, l, weir.EvRequest, "miss")
			if miss.Duration != delay || miss.Status != http.StatusOK || miss.Partition == "" || miss.Info.Fwd != weir.FwdURIMiss || !miss.Info.Stored {
				t.Errorf("miss event = %+v, want the fetch's duration, status 200, a partition and the response's CacheInfo", miss)
			}
			seen.want(t, l, weir.EvFetchStart, "foreground")
			if end := seen.want(t, l, weir.EvFetchEnd, "foreground"); end.Duration != delay || end.Status != http.StatusOK {
				t.Errorf("fetch-end event = %+v, want duration %v and status 200", end, delay)
			}
			serve(t, e, getReq("/a"), o)
			if hit := seen.want(t, l, weir.EvRequest, "hit"); !hit.Info.Hit {
				t.Errorf("hit event Info = %+v, want Hit", hit.Info)
			}

			serve(t, e, getReq("/big"), o)
			if _, _, err := serveResult(t, e, getReq("/down"), o); err != nil {
				t.Fatalf("/down: %v", err)
			}
			if _, _, err := serveResult(t, e, getReq("/cut"), o); err == nil {
				t.Fatal("/cut: want the truncated body's error")
			}
			seen.want(t, l, weir.EvNotStored, "too-large", "status", "incomplete")
			if ev := seen.want(t, l, weir.EvRequest, "error"); ev.Status != http.StatusBadGateway {
				t.Errorf("error event status = %d, want 502", ev.Status)
			}
			serve(t, e, getReq("/down"), o)
			seen.want(t, l, weir.EvNegativeServed)
			seen.want(t, l, weir.EvRequest, "negative")

			serve(t, e, getReq("/etag"), o)
			serve(t, e, getReq("/swr"), o)
			time.Sleep(2 * time.Second)
			serve(t, e, getReq("/etag"), o)
			seen.want(t, l, weir.EvRequest, "revalidated")
			serve(t, e, getReq("/swr"), o)
			synctest.Wait()
			seen.want(t, l, weir.EvStaleServed, "swr")
			seen.want(t, l, weir.EvRequest, "stale")
			seen.want(t, l, weir.EvFetchStart, "background")
			seen.want(t, l, weir.EvFetchEnd, "background")

			serve(t, e, postReq("/a"), o)
			seen.want(t, l, weir.EvRequest, "pass")
			seen.want(t, l, weir.EvFetchStart, "pass")
			seen.want(t, l, weir.EvFetchEnd, "pass")
			seen.want(t, l, weir.EvPurge, "invalid")

			bad := getReq("/a")
			bad.Scheme = "ftp"
			if _, err := e.Serve(t.Context(), bad, o); err == nil {
				t.Fatal("ftp scheme: want a rejection")
			}
			seen.want(t, l, weir.EvKeyRejected)
			// One EvRequest per Serve return, refusals included; a request
			// refused by classification has no partition.
			if n := l.count(weir.EvRequest); n != 12 {
				t.Errorf("EvRequest events = %d, want 12, one per Serve call so far", n)
			}
			l.mu.Lock()
			last := l.evs[len(l.evs)-1]
			l.mu.Unlock()
			if last.Kind != weir.EvRequest || last.Reason != "error" || last.Partition != "" || last.Status != http.StatusBadRequest {
				t.Errorf("rejected request's event = %+v, want request/error, status 400, no partition", last)
			}

			if st, err := e.Warm(t.Context(), warmReqs("/w"), o); err != nil || st.Fetched != 1 {
				t.Fatalf("Warm = %+v, %v", st, err)
			}
			seen.want(t, l, weir.EvFetchStart, "warm")
			seen.want(t, l, weir.EvFetchEnd, "warm")

			if err := e.Purge(t.Context(), weir.Purge{All: true}); err != nil {
				t.Fatalf("Purge: %v", err)
			}
			seen.want(t, l, weir.EvPurge, "soft")
			weir.MissWindow(e, time.Now(), getReq("/m"))
			seen.want(t, l, weir.EvMissRateAnomaly)

			if err := e.SetMode(weir.ModeBypass, time.Minute); err != nil {
				t.Fatalf("SetMode: %v", err)
			}
			seen.want(t, l, weir.EvMode, "bypass")
			serve(t, e, getReq("/a"), o)
			seen.want(t, l, weir.EvRequest, "bypass")
		})

	scenario("coalescing", weir.Config{Coalesce: weir.CoalesceConfig{FollowerMaxWait: time.Second}},
		func(t *testing.T, cfg weir.Config, l *eventLog) {
			o := testorigin.NewChecked(t, 64, 16)
			o.Route("/s", ccBehavior(http.StatusOK, "max-age=1, stale-if-error=600", "s"))
			e := newEngine(t, cfg)
			defer closeEngine(t, e)

			serve(t, e, getReq("/s"), o)
			time.Sleep(2 * time.Second)
			gate := make(chan struct{})
			o.Default(testorigin.Behavior{Gate: gate})
			o.Route("/s", testorigin.Behavior{Gate: gate})
			lead := serveTimed(t, e, getReq("/c"), o)
			synctest.Wait()
			follow := serveTimed(t, e, getReq("/c"), o)
			stale := serveTimed(t, e, getReq("/s"), o)
			synctest.Wait()
			seen.want(t, l, weir.EvCoalesceJoin)
			time.Sleep(time.Second + time.Millisecond)
			synctest.Wait()
			// The follower fetches for itself; the creator of /s has a stale
			// entry to serve instead (FR-COA-4).
			seen.want(t, l, weir.EvCoalesceTimeout, "direct", "stale")
			seen.want(t, l, weir.EvStaleServed, "coalesce-timeout")
			// The creator of /c keeps waiting on its own fetch: no event.
			if n := l.count(weir.EvCoalesceTimeout); n != 2 {
				t.Errorf("EvCoalesceTimeout events = %d, want 2", n)
			}
			close(gate)
			for _, ch := range []<-chan timedServe{lead, follow, stale} {
				if r := <-ch; r.err != nil {
					t.Fatalf("request: %v", r.err)
				}
			}
		})

	scenario("shed background refresh", weir.Config{
		Rand:     func() float64 { return math.Nextafter(1, 0) },
		Limiter:  weir.LimiterConfig{MaxConcurrent: 4, ReserveForeground: 1},
		Timeouts: weir.TimeoutsConfig{Origin: 2 * time.Minute},
	}, func(t *testing.T, cfg weir.Config, l *eventLog) {
		o := testorigin.NewChecked(t, 4, 4)
		o.Default(cacheable("v"))
		gate := make(chan struct{})
		o.Route("/busy", testorigin.Behavior{Gate: gate})
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		serve(t, e, getReq("/hot"), o)
		busy := make([]<-chan timedServe, 3) // MaxConcurrent - ReserveForeground
		for i := range busy {
			busy[i] = serveTimed(t, e, postReq("/busy"), o)
		}
		synctest.Wait()
		time.Sleep(60*time.Second - time.Millisecond) // 1ms left: the refresh triggers
		serve(t, e, getReq("/hot"), o)
		synctest.Wait()
		seen.want(t, l, weir.EvShed, "background")
		seen.want(t, l, weir.EvRefreshDropped, "no-slot")
		close(gate)
		for _, ch := range busy {
			if r := <-ch; r.err != nil {
				t.Fatalf("busy request: %v", r.err)
			}
		}
	})

	scenario("origin breaker", weir.Config{}, func(t *testing.T, cfg weir.Config, l *eventLog) {
		o := testorigin.NewChecked(t, 64, 16)
		o.SetDown(true)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 25 {
			if _, _, err := serveResult(t, e, getReq(fmt.Sprintf("/f%d", i)), o); err == nil {
				t.Fatalf("request %d: want an error from a down origin", i)
			}
		}
		seen.want(t, l, weir.EvBreakerState, "open")
		if st := e.Stats(); st.BreakerState != weir.BreakerOpen {
			t.Errorf("Stats().BreakerState = %v, want BreakerOpen", st.BreakerState)
		}
		// FR-OBS-2: a poll reports the ended open period without moving the
		// breaker, so Stats never calls the Observer.
		time.Sleep(time.Minute)
		if st := e.Stats(); st.BreakerState != weir.BreakerHalfOpen {
			t.Errorf("Stats().BreakerState after the open period = %v, want BreakerHalfOpen", st.BreakerState)
		}
		if n := l.count(weir.EvBreakerState); n != 1 {
			t.Errorf("EvBreakerState events = %d, want 1: Stats must not emit", n)
		}
	})

	scenario("store outage", weir.Config{}, func(t *testing.T, cfg weir.Config, l *eventLog) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable("v"))
		s := newFaultyStore(t, true)
		s.fail.Store(true)
		cfg.Store = s
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 50 {
			serve(t, e, getReq(fmt.Sprintf("/k%d", i)), o)
		}
		seen.want(t, l, weir.EvStoreError, "get")
		seen.want(t, l, weir.EvStoreBreaker, "open")
	})

	scenario("variant cap", weir.Config{
		Forward: weir.ForwardConfig{Allow: []string{"X-Custom"}},
		Key:     weir.KeyConfig{MaxVariants: 2},
	}, func(t *testing.T, cfg weir.Config, l *eventLog) {
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(varyBody("X-Custom", 0))
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for _, v := range []string{"a", "b", "c"} {
			serve(t, e, varyReq("/o", "X-Custom", v), o)
		}
		seen.want(t, l, weir.EvVaryOverflow)
	})

	// The engine's own memory store reports evictions through the observer.
	// GOMEMLIMIT at its floor gives a 16 MiB store, which 24 objects of
	// 900 KiB overfill.
	scenario("memory store eviction", weir.Config{}, func(t *testing.T, cfg weir.Config, l *eventLog) {
		// Process-wide; safe while no test in this package runs in parallel.
		defer debug.SetMemoryLimit(debug.SetMemoryLimit(1))
		o := testorigin.NewChecked(t, 64, 16)
		o.Default(cacheable(strings.Repeat("x", 900<<10)))
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		for i := range 24 {
			serve(t, e, getReq(fmt.Sprintf("/e%d", i)), o)
		}
		if ev := seen.want(t, l, weir.EvEvict); ev.Status < 1 {
			t.Errorf("evict event = %+v, want the number of records evicted in Status", ev)
		}
	})

	for k := weir.EventKind(1); k.String() != "unknown"; k++ {
		if !seen[k] {
			t.Errorf("no scenario made the observer receive %v", k)
		}
	}
}

// 01 §4 Stats, 04 §9.2: gauges come from the limiter, the breaker and the
// store at the time of the call.
func TestStats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 1, 1)
		o.Default(cacheable("v"))
		gate := make(chan struct{})
		o.Route("/g1", testorigin.Behavior{Gate: gate})
		o.Route("/g2", testorigin.Behavior{Gate: gate})
		cfg := cacheCfg
		cfg.Limiter = weir.LimiterConfig{MaxConcurrent: 1, MaxQueue: 10}
		e := newEngine(t, cfg)
		defer closeEngine(t, e)

		if st := e.Stats(); st != (weir.EngineStats{}) {
			t.Fatalf("idle Stats = %+v, want zero gauges and an empty store", st)
		}
		serve(t, e, getReq("/a"), o)
		if st := e.Stats(); st.StoreBytes <= 0 || st.Inflight != 0 {
			t.Fatalf("after one stored response Stats = %+v, want StoreBytes > 0 and nothing in flight", st)
		}
		first := serveTimed(t, e, getReq("/g1"), o)
		synctest.Wait()
		second := serveTimed(t, e, getReq("/g2"), o)
		synctest.Wait()
		if st := e.Stats(); st.Inflight != 1 || st.Queued != 1 || st.BreakerState != weir.BreakerClosed {
			t.Fatalf("Stats = %+v, want 1 in flight, 1 queued, breaker closed", st)
		}
		close(gate)
		for _, ch := range []<-chan timedServe{first, second} {
			if r := <-ch; r.err != nil {
				t.Fatalf("gated request: %v", r.err)
			}
		}
	})
	t.Run("store without Sizer reports -1", func(t *testing.T) {
		cfg := cacheCfg
		cfg.Store = newFaultyStore(t, false)
		e := newEngine(t, cfg)
		defer closeEngine(t, e)
		if st := e.Stats(); st.StoreBytes != -1 {
			t.Fatalf("StoreBytes = %d, want -1", st.StoreBytes)
		}
	})
}
