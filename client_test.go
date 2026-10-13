package weir_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AshwinSathian/weir"
	"github.com/AshwinSathian/weir/internal/testorigin"
)

func clientReq(client, path string) *weir.Request {
	r := getReq(path)
	r.Client = client
	return r
}

func perClient(n int) weir.Config {
	cfg := cacheCfg
	cfg.Limiter.MaxPerClient = n
	return cfg
}

// FR-FAIR-4, T-11: a flood of distinct paths from one client holds at most
// MaxPerClient origin slots, and another client's miss is served. With the
// cap off, the same flood fills every slot and the queue and that miss is
// shed (TestClientFloodWithoutCap).
func TestClientFloodLeavesOthersServed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.NewChecked(t, 9, 16)
		o.Default(testorigin.Behavior{Delay: 5 * time.Second, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		e := newEngine(t, perClient(8))
		defer closeEngine(t, e)

		var wg sync.WaitGroup
		for i := range 1100 {
			wg.Go(func() {
				if resp, err := e.Serve(t.Context(), clientReq("203.0.113.9", "/flood/"+strconv.Itoa(i)), o); err == nil {
					resp.Body.Close()
				}
			})
		}
		synctest.Wait()
		r := <-serveTimed(t, e, clientReq("198.51.100.7", "/legit"), o)
		if r.err != nil || r.elapsed != 5*time.Second {
			t.Fatalf("another client's miss: %v after %v, want served after one origin delay", r.err, r.elapsed)
		}
		wg.Wait()
		if n := o.MaxInflight(); n != 9 {
			t.Fatalf("origin calls in flight = %d, want the flooding client's 8 and the other client's 1", n)
		}
	})
}

// T-11: the flood without the cap, for the difference.
func TestClientFloodWithoutCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := testorigin.New()
		o.Default(testorigin.Behavior{Delay: 5 * time.Second, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		e := newEngine(t, cacheCfg)
		defer closeEngine(t, e)

		var wg sync.WaitGroup
		for i := range 1100 {
			wg.Go(func() {
				if resp, err := e.Serve(t.Context(), clientReq("203.0.113.9", "/flood/"+strconv.Itoa(i)), o); err == nil {
					resp.Body.Close()
				}
			})
		}
		synctest.Wait()
		if r := <-serveTimed(t, e, clientReq("198.51.100.7", "/legit"), o); !errors.Is(r.err, weir.ErrShed) {
			t.Fatalf("another client's miss with the cap off: %v, want ErrShed", r.err)
		}
		wg.Wait()
	})
}

// FR-FAIR-4: a client at its cap still gets its hits at once, and a request
// that names no client is never capped.
func TestClientCapCountsOnlyOriginWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		o.Route("/cached", cacheable("hit"))
		e := newEngine(t, perClient(2))
		defer closeEngine(t, e)

		serve(t, e, clientReq("203.0.113.9", "/cached"), o)
		a := serveTimed(t, e, clientReq("203.0.113.9", "/a"), o)
		b := serveTimed(t, e, clientReq("203.0.113.9", "/b"), o)
		synctest.Wait()
		if r := <-serveTimed(t, e, clientReq("203.0.113.9", "/cached"), o); r.err != nil || r.elapsed != 0 {
			t.Fatalf("hit for a client at its cap: %v after %v, want served at once", r.err, r.elapsed)
		}
		over := serveTimed(t, e, clientReq("203.0.113.9", "/c"), o)
		var anon []<-chan timedServe
		for i := range 5 {
			anon = append(anon, serveTimed(t, e, getReq("/anon"+strconv.Itoa(i)), o))
		}
		synctest.Wait()
		if n := o.TotalCalls(); n != 1+2+5 {
			t.Fatalf("origin calls = %d, want the cached one, the client's 2 and 5 from requests without a client", n)
		}
		if r := <-over; !errors.Is(r.err, weir.ErrShed) || r.elapsed != 5*time.Second {
			t.Fatalf("third request of the client: %v after %v, want ErrShed after MaxQueueWait", r.err, r.elapsed)
		}
		close(gate)
		for _, ch := range append(anon, a, b) {
			if r := <-ch; r.err != nil {
				t.Fatal(r.err)
			}
		}
	})
}

// FR-FAIR-4, FR-COA-6: a client over its cap waits before it joins or creates
// a flight, so it never leads a fetch whose shed every other waiter would
// share.
func TestClientOverCapNeverLeadsFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		o.Route("/hot", testorigin.Behavior{Delay: 3 * time.Second, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("hot")})
		e := newEngine(t, perClient(1))
		defer closeEngine(t, e)
		defer close(gate)

		held := serveTimed(t, e, clientReq("203.0.113.9", "/held"), o)
		synctest.Wait()
		first := serveTimed(t, e, clientReq("203.0.113.9", "/hot"), o) // over the cap: waits at the gate
		synctest.Wait()
		legit := <-serveTimed(t, e, clientReq("198.51.100.7", "/hot"), o)
		if legit.err != nil || legit.elapsed != 3*time.Second {
			t.Fatalf("other client on the hot key: %v after %v, want its own flight's answer", legit.err, legit.elapsed)
		}
		if r := <-first; !errors.Is(r.err, weir.ErrShed) {
			t.Fatalf("the capped client's request: %v, want ErrShed", r.err)
		}
		_ = held
	})
}

// FR-FAIR-4: an IPv6 client is its /64.
func TestClientIPv6CountsPerPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		e := newEngine(t, perClient(1))
		defer closeEngine(t, e)
		defer close(gate)

		serveTimed(t, e, clientReq("2001:db8:1:2::1", "/a"), o)
		synctest.Wait()
		same := serveTimed(t, e, clientReq("2001:db8:1:2:ffff::9", "/b"), o)
		other := serveTimed(t, e, clientReq("2001:db8:1:3::1", "/c"), o)
		synctest.Wait()
		if n := o.TotalCalls(); n != 2 {
			t.Fatalf("origin calls = %d, want 2: one per /64", n)
		}
		if r := <-same; !errors.Is(r.err, weir.ErrShed) {
			t.Fatalf("second address in the same /64: %v, want ErrShed", r.err)
		}
		_ = other
	})
}

// FR-FAIR-4, INV-1: the client's name reaches the Origin only with a request
// that is passed through. A cacheable fetch, whose response others share,
// never carries it.
func TestClientNotForwardedOnCacheableFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		seen := map[string]string{}
		o := weir.OriginFunc(func(_ context.Context, r *weir.Request) (*weir.Response, error) {
			seen[r.Method] = r.Client
			return &weir.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: http.NoBody}, nil
		})
		e := newEngine(t, perClient(4))
		defer closeEngine(t, e)

		serve(t, e, clientReq("203.0.113.9", "/a"), o)
		post := postReq("/a")
		post.Client = "203.0.113.9"
		serve(t, e, post, o)
		if seen["GET"] != "" || seen["POST"] != "203.0.113.9" {
			t.Fatalf("Client at the origin: GET %q, POST %q; want empty and the client", seen["GET"], seen["POST"])
		}
	})
}

// FR-FAIR-4, FR-STL-2: a request shed at its client's cap is answered from a
// stale entry that stale-if-error permits, like any other shed.
func TestClientCapServesStale(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		o := testorigin.New()
		o.Default(testorigin.Behavior{Gate: gate, Header: http.Header{"Cache-Control": {"max-age=60"}}, Body: []byte("ok")})
		o.Route("/s", testorigin.Behavior{Header: http.Header{"Cache-Control": {"max-age=1, stale-if-error=600"}}, Body: []byte("old")})
		e := newEngine(t, perClient(1))
		defer closeEngine(t, e)
		defer close(gate)

		serve(t, e, clientReq("203.0.113.9", "/s"), o)
		time.Sleep(3 * time.Second)
		serveTimed(t, e, clientReq("203.0.113.9", "/held"), o)
		synctest.Wait()
		resp, body := serve(t, e, clientReq("203.0.113.9", "/s"), o)
		if body != "old" || resp.Cache.Stale != weir.StaleShed {
			t.Fatalf("got %q stale=%v, want the stale entry served as shed", body, resp.Cache.Stale)
		}
	})
}
