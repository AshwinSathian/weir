package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	if err := r.reset(ctx); err != nil {
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
	r.want = int64(ok.BodyBytes) // the healthy profile's body, not the failing one's
	lat := r.fire(ctx, good, out.Status)
	finish(out, lat)
	lost := 0
	for code, n := range out.Status {
		if code != "200" {
			lost += n
		}
	}
	note(out, "bystander_failures", float64(lost))
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
	if err := r.reset(ctx); err != nil {
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
	// The flood's own statuses are kept: a subject that refused the POSTs
	// was never flooded, and its "0 entries lost" would mean nothing.
	var post2xx, postOther atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range next {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"-flood?r="+strconv.Itoa(i), nil)
				if err != nil {
					continue
				}
				r.sent.Add(1)
				resp, err := r.client.Do(req)
				if err != nil {
					postOther.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode/100 == 2 {
					post2xx.Add(1)
				} else {
					postOther.Add(1)
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
	if err := r.reset(ctx); err != nil {
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
	note(out, "post_2xx", float64(post2xx.Load()))
	note(out, "post_not_2xx", float64(postOther.Load()))
	return nil
}

func failed(code string) bool { return !strings.HasPrefix(code, "2") }

// bustingFlood: an origin that serves 128 requests at once and refuses the
// rest. For 5 s, every 100 ms, 200 requests ask for one path with a query
// string nobody has used (a cache-busting flood, 2 000 a second) and 20 ask
// for 20 other paths nobody has asked for yet (ordinary cold traffic, 200 a
// second). The flood alone is more than the origin can serve. The row counts
// the 1 000 ordinary requests; the flood's own failures are in the notes,
// because a cache that saves the bystanders by refusing the flood has still
// refused clients (docs/11 R5).
func bustingFlood(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	const ticks, floodPerTick, byPerTick = 50, 200, 20
	by := sc.Name + "-by"
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+by, profile{DelayMS: sc.Prof.DelayMS, CacheControl: sc.Prof.CacheControl, BodyBytes: sc.Prof.BodyBytes, Pool: sc.Name}, nil); err != nil {
		return err
	}
	if err := r.reset(ctx); err != nil {
		return err
	}
	byBase := strings.Replace(base, "/o/"+sc.Name+"/", "/o/"+by+"/", 1)
	var (
		mu          sync.Mutex
		lat         []time.Duration
		floodFailed int
		wg          sync.WaitGroup
	)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for t := range ticks {
		// One burst per tick, a bystander after every ten flood requests,
		// so neither kind reaches the cache ahead of the other.
		const every = floodPerTick/byPerTick + 1
		urls := make([]string, floodPerTick+byPerTick)
		for i := range urls {
			if i%every == every-1 {
				urls[i] = byBase + "-" + strconv.Itoa(t*byPerTick+i/every)
			} else {
				urls[i] = base + "-flood?bust=" + strconv.Itoa(t*floodPerTick+i-i/every)
			}
		}
		wg.Go(func() {
			got, l := r.fireAll(ctx, urls)
			mu.Lock()
			defer mu.Unlock()
			for i, g := range got {
				if i%every == every-1 {
					lat = append(lat, l[i])
					out.Status[g.code]++
				} else if failed(g.code) {
					floodFailed++
				}
			}
		})
		select {
		case <-tick.C:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
	}
	wg.Wait()
	finish(out, lat)
	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return err
	}
	bad := 0
	for code, n := range out.Status {
		if failed(code) {
			bad += n
		}
	}
	note(out, "bystander_failures", float64(bad))
	note(out, "bystander_origin_calls", float64(st[by].Calls))
	note(out, "bystander_origin_refused", float64(st[by].Refused))
	note(out, "flood_sent", ticks*floodPerTick)
	note(out, "flood_failures", float64(floodFailed))
	return nil
}

// brownout: 200 URLs with a 2 s lifetime under steady traffic, 200 requests
// a second for 60 s, each URL once a second. The origin is healthy for 5 s,
// answers 503 for 10 s, is healthy for 5 s, answers 503 for 10 s, and is
// healthy for the last 30 s, long enough to see a breaker that backed off
// close again. A request belongs to the phase it was sent in.
// The notes count failures in the two bursts and outside them, the origin
// calls made during the bursts, and how long after the second burst the
// last failing request was sent.
func brownout(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	const keys, perTick = 200, 20
	// The tick (100 ms each) at which each phase ends; phases 1 and 3 are bursts.
	ends := []int{50, 150, 200, 300, 600}
	urls := make([]string, keys)
	for i := range urls {
		urls[i] = base + "-" + strconv.Itoa(i)
	}
	for chunk := range slices.Chunk(urls, 64) {
		r.fire(ctx, chunk, nil)
	}
	if err := r.reset(ctx); err != nil {
		return err
	}
	down := sc.Prof
	down.Status, down.CacheControl, down.DelayMS = http.StatusServiceUnavailable, "", 100
	calls := func() (int64, error) {
		var st map[string]counters
		err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st)
		return st[sc.Name].Calls, err
	}
	var (
		mu                                    sync.Mutex
		wg                                    sync.WaitGroup
		lat                                   []time.Duration
		burstSent, burstFailed, outsideFailed int
		burstCalls, atBurstStart              int64
		recovered                             time.Time     // when the second burst ended
		lastFailure                           time.Duration // after recovered, by send time
	)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	phase := 0
	for t := range ends[len(ends)-1] {
		if t == ends[phase] {
			phase++
			p := sc.Prof
			if phase%2 == 1 {
				p = down
			}
			if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, p, nil); err != nil {
				return err
			}
			n, err := calls()
			if err != nil {
				return err
			}
			if phase%2 == 1 {
				atBurstStart = n
			} else {
				burstCalls += n - atBurstStart
			}
			if phase == 4 {
				recovered = time.Now()
			}
		}
		burst, final := phase%2 == 1, phase == 4
		for i := range perTick {
			u := urls[(t*perTick+i)%keys]
			wg.Go(func() {
				sent := time.Now()
				code := r.get(ctx, u)
				mu.Lock()
				defer mu.Unlock()
				lat = append(lat, time.Since(sent))
				out.Status[code]++
				switch {
				case burst:
					burstSent++
					if failed(code) {
						burstFailed++
					}
				case failed(code):
					outsideFailed++
					if final {
						lastFailure = max(lastFailure, sent.Sub(recovered))
					}
				}
			})
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		}
	}
	wg.Wait()
	finish(out, lat)
	note(out, "burst_requests", float64(burstSent))
	note(out, "burst_failures", float64(burstFailed))
	note(out, "failures_outside_bursts", float64(outsideFailed))
	note(out, "origin_calls_in_bursts", float64(burstCalls))
	note(out, "last_failure_s_after_recovery", lastFailure.Round(100*time.Millisecond).Seconds())
	return nil
}

// sendPurge tells the subject to drop paths, the way its configuration
// allows (runner.purge). A spec with "{path}" in its URL is sent once per
// path, 64 at a time; one without is sent once.
func (r *runner) sendPurge(ctx context.Context, paths []string) error {
	f := strings.SplitN(r.purge, " ", 3)
	if len(f) < 2 {
		return fmt.Errorf("compare: purge spec %q is not \"METHOD URL [BODY]\"", r.purge)
	}
	one := func(url string) error {
		var body io.Reader
		if len(f) == 3 {
			body = strings.NewReader(f[2])
		}
		req, err := http.NewRequestWithContext(ctx, f[0], url, body)
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("compare: purge %s %s: %s %s", f[0], url, resp.Status, b)
		}
		return nil
	}
	if !strings.Contains(f[1], "{path}") {
		return one(f[1])
	}
	for chunk := range slices.Chunk(paths, 64) {
		errs := make([]error, len(chunk))
		var wg sync.WaitGroup
		for i, p := range chunk {
			wg.Go(func() { errs[i] = one(strings.Replace(f[1], "{path}", p, 1)) })
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}
	return nil
}

// purgeHerd: 1 000 URLs are cached for 10 minutes. One wave reads them (all
// hits), the cache is told to purge them, and two more waves follow, at once
// and 1 s later. After 3 s more every URL is read once again. The row counts
// the two waves after the purge. A purge that deletes makes the next wave a
// cold start on every key; one that marks entries stale keeps serving the
// old copy while it refetches, so the notes say how many answers still came
// from before the purge, and how many URLs had not been refreshed by the
// last read: a cache that ignored the purge would otherwise show the best
// origin numbers of all.
func purgeHerd(ctx context.Context, r *runner, sc scenario, base string, out *row) error {
	if r.purge == "" {
		out.NotAvailable = "no purge in this configuration"
		return nil
	}
	const keys = 1000
	urls, paths := make([]string, keys), make([]string, keys)
	for i := range urls {
		urls[i] = base + "-" + strconv.Itoa(i)
		paths[i] = strings.TrimPrefix(urls[i], r.target)
	}
	var before int64 // the last origin response made before the purge
	old := func(got []reply) (n int) {
		for _, g := range got {
			if !failed(g.code) && g.seq <= before {
				n++
			}
		}
		return n
	}
	for chunk := range slices.Chunk(urls, 64) {
		got, _ := r.fireAll(ctx, chunk)
		for _, g := range got {
			before = max(before, g.seq)
		}
	}
	if err := r.reset(ctx); err != nil {
		return err
	}
	hits, _ := r.fireAll(ctx, urls)
	for _, g := range hits {
		before = max(before, g.seq)
	}
	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return err
	}
	note(out, "origin_calls_before_purge", float64(st[sc.Name].Calls))
	if err := r.sendPurge(ctx, paths); err != nil {
		return err
	}
	var lat []time.Duration
	for w := range 2 {
		if w > 0 {
			sleep(ctx, time.Second)
		}
		got, l := r.fireAll(ctx, urls)
		lat = append(lat, l...)
		for _, g := range got {
			out.Status[g.code]++
		}
		note(out, "served_from_before_purge_wave_"+strconv.Itoa(w+1), float64(old(got)))
	}
	finish(out, lat)
	sleep(ctx, 3*time.Second)
	stale := 0
	for chunk := range slices.Chunk(urls, 64) {
		got, _ := r.fireAll(ctx, chunk)
		stale += old(got)
	}
	note(out, "not_refreshed_at_last_read", float64(stale))
	return nil
}

// restart: 2 000 URLs are cached for 10 minutes, the cache is stopped and
// started again, and the URLs are read once more, 64 at a time. The origin
// calls of that replay are the entries the restart lost.
func restart(ctx context.Context, r *runner, _ scenario, base string, out *row) error {
	if r.restart == nil {
		out.NotAvailable = "the runner was not given a way to restart the subject"
		return nil
	}
	urls := make([]string, 2000)
	for i := range urls {
		urls[i] = base + "-" + strconv.Itoa(i)
	}
	for chunk := range slices.Chunk(urls, 64) {
		r.fire(ctx, chunk, nil)
	}
	sleep(ctx, 2*time.Second)
	if err := r.restart(ctx); err != nil {
		return err
	}
	if err := r.ready(ctx); err != nil {
		return err
	}
	if err := r.reset(ctx); err != nil {
		return err
	}
	var lat []time.Duration
	for chunk := range slices.Chunk(urls, 64) {
		lat = append(lat, r.fire(ctx, chunk, out.Status)...)
	}
	finish(out, lat)
	return nil
}
