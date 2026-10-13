package main

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// finish fills the row's request count and latency figures from lat.
func finish(out *row, lat []time.Duration) {
	slices.Sort(lat)
	out.Requests = len(lat)
	if len(lat) == 0 {
		return
	}
	out.P50MS, out.P99MS, out.MaxMS = ms(lat[len(lat)/2]), ms(lat[len(lat)*99/100]), ms(lat[len(lat)-1])
}

func repeat(u string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = u
	}
	return out
}

func note(out *row, k string, v float64) {
	if out.Notes == nil {
		out.Notes = map[string]float64{}
	}
	out.Notes[k] = v
}

// turnedPrivate: a URL is cached with a short lifetime, expires, and the
// origin then answers it "private". Three waves of 100 clients follow. The
// figure is how long clients wait once the cache knows the URL is not
// storable: one origin delay is the floor, two means every wave still waits
// on a shared fetch and then fetches again.
func turnedPrivate(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	u := base + "-k"
	r.fire(ctx, []string{u}, nil)
	sleep(ctx, 3*time.Second)
	p := sc.Prof
	p.CacheControl = "private"
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, p, nil); err != nil {
		return err
	}
	if err := r.ctl(ctx, http.MethodPost, "/reset", nil, nil); err != nil {
		return err
	}
	var lat []time.Duration
	for w := range 3 {
		if w > 0 {
			sleep(ctx, time.Second)
		}
		l := r.fire(ctx, repeat(u, 100), out.Status)
		if w == 2 {
			s := slices.Clone(l)
			slices.Sort(s)
			note(out, "wave3_p50_ms", ms(s[len(s)/2]))
		}
		lat = append(lat, l...)
	}
	finish(out, lat)
	return nil
}

// badRoute: one path answers 504, asked 60 times with distinct query strings
// (so nothing caches the error under one key), while another path is
// healthy. Then 200 clients ask for 200 distinct healthy URLs. The row
// counts the healthy requests only: a cache whose breaker judged the whole
// origin by one route fails them.
func badRoute(ctx context.Context, r *runner, _ scenario, base string, out *row) error {
	ok := profile{DelayMS: 20, CacheControl: "max-age=60", BodyBytes: 1024}
	if err := r.ctl(ctx, http.MethodPut, "/profile/bad-route-ok", ok, nil); err != nil {
		return err
	}
	okBase := r.target + "/o/bad-route-ok/" + strconv.FormatInt(time.Now().UnixNano(), 36)
	// A breaker judges a window of recent fetches (Weir: 10 s). Without this
	// pause the scenario before this one fills that window with successes
	// and the 60 failures below are a minority: the first run measured the
	// previous scenario, not this one.
	sleep(ctx, 11*time.Second)
	r.fire(ctx, []string{okBase + "-warm"}, nil) // one success before the failures
	bad := make([]string, 60)
	for i := range bad {
		bad[i] = base + "-report?q=" + strconv.Itoa(i)
	}
	for chunk := range slices.Chunk(bad, 6) {
		r.fire(ctx, chunk, nil)
	}
	good := make([]string, 200)
	for i := range good {
		good[i] = okBase + "-" + strconv.Itoa(i)
	}
	lat := r.fire(ctx, good, out.Status)
	finish(out, lat)
	failed := 0
	for code, n := range out.Status {
		if code != "200" {
			failed += n
		}
	}
	note(out, "bystander_failures", float64(failed))
	return nil
}

// originDown: 200 URLs are cached for 2 s and expire. The origin then
// answers 503 to everything for three waves, one second apart, and recovers
// for a fourth. The row counts the three outage waves: how many clients
// still got a 2xx (a stale copy), and how often the dead origin was called.
// The note is how many of the 200 got a 2xx in the wave after recovery.
func originDown(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	keys := make([]string, 200)
	for i := range keys {
		keys[i] = base + "-" + strconv.Itoa(i)
	}
	for chunk := range slices.Chunk(keys, 64) {
		r.fire(ctx, chunk, nil)
	}
	sleep(ctx, 3*time.Second)
	down := sc.Prof
	down.Status, down.CacheControl = http.StatusServiceUnavailable, ""
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, down, nil); err != nil {
		return err
	}
	if err := r.ctl(ctx, http.MethodPost, "/reset", nil, nil); err != nil {
		return err
	}
	var lat []time.Duration
	for w := range 3 {
		if w > 0 {
			sleep(ctx, time.Second)
		}
		lat = append(lat, r.fire(ctx, keys, out.Status)...)
	}
	finish(out, lat)
	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return err
	}
	note(out, "origin_calls_while_down", float64(st[sc.Name].Calls))
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, sc.Prof, nil); err != nil {
		return err
	}
	// Recovery, 1 s and 10 s after the origin is back: a breaker may keep
	// refusing for a while, which is its purpose and its cost.
	for _, at := range []struct {
		wait time.Duration
		name string
	}{{time.Second, "ok_1s_after_recovery"}, {9 * time.Second, "ok_10s_after_recovery"}} {
		sleep(ctx, at.wait)
		after := map[string]int{}
		r.fire(ctx, keys, after)
		note(out, at.name, float64(after["200"]))
	}
	return nil
}

// leaderAbort: the first client asks for a slow URL (2 s) and hangs up after
// 500 ms. Fifty more ask for it 100 ms after the first. The row counts the
// fifty: a cache that tied the fetch to its first client fails or refetches.
func leaderAbort(ctx context.Context, r *runner, _ scenario, base string, out *row) error {
	u := base + "-k"
	lctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { r.get(lctx, u) })
	sleep(ctx, 100*time.Millisecond)
	lat := r.fire(ctx, repeat(u, 50), out.Status)
	wg.Wait()
	finish(out, lat)
	return nil
}

// postFlood: 2 000 URLs are cached for 10 minutes. Then 300 000 POSTs go to
// distinct URLs nobody cached, each answered 200. Then the 2 000 are asked
// for again. The note is how many of them went back to the origin: an
// unsafe request must invalidate its own URL (RFC 9111 §4.4) and no other.
func postFlood(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	const cached, flood, workers = 2000, 300000, 64
	keys := make([]string, cached)
	for i := range keys {
		keys[i] = base + "-" + strconv.Itoa(i)
	}
	for chunk := range slices.Chunk(keys, 64) {
		r.fire(ctx, chunk, nil)
	}
	sleep(ctx, 2*time.Second)
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range next {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"-flood?r="+strconv.Itoa(i), nil)
				if err != nil {
					continue
				}
				if resp, err := r.client.Do(req); err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}
		})
	}
	for i := range flood {
		select {
		case next <- i:
		case <-ctx.Done():
		}
	}
	close(next)
	wg.Wait()
	sleep(ctx, 2*time.Second)
	if err := r.ctl(ctx, http.MethodPost, "/reset", nil, nil); err != nil {
		return err
	}
	var lat []time.Duration
	for chunk := range slices.Chunk(keys, 64) {
		lat = append(lat, r.fire(ctx, chunk, out.Status)...)
	}
	finish(out, lat)
	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return err
	}
	note(out, "entries_lost", float64(st[sc.Name].Calls))
	return nil
}
