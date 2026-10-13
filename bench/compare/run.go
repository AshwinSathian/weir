package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// scenario is one origin-protection case of docs/11 §5: an origin profile
// and a burst shape. Every wave sends Keys x Clients requests at one instant.
type scenario struct {
	Name    string
	Prof    profile
	Keys    int
	Clients int           // per key, per wave
	Prime   time.Duration // above zero: fetch every key once, zero the counters, wait this long, then measure
	Waves   int
	Gap     time.Duration // between waves
	// Settle is how long the runner waits after the last wave before it
	// reads the origin's counters, so refreshes a cache started in the
	// background are counted (docs/11 §10, first round).
	Settle time.Duration
	// Reps, above zero, caps the repetitions of a scenario that is too long
	// to repeat (its result is a count, not a timing).
	Reps int
	// Custom, when set, runs the scenario instead of the burst shape above
	// and fills the row's requests, status, latencies and notes itself.
	Custom func(ctx context.Context, r *runner, sc scenario, base string, out *row) error
}

// scenarios are the count-based cases of docs/11 §5.1 and §5.2.
var scenarios = []scenario{
	{Name: "cold-herd", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 1024}, Keys: 1, Clients: 1000, Waves: 1},
	{Name: "cold-keys", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 1024}, Keys: 1000, Clients: 2, Waves: 1},
	// Two waves: a cache that answers the first from stale copies refreshes
	// in the background, and the second wave, 2 s later, asks again for
	// whatever it left stale.
	{Name: "sync-expiry", Prof: profile{DelayMS: 20, CacheControl: "max-age=5", BodyBytes: 1024}, Keys: 5000, Clients: 1, Prime: 6 * time.Second, Waves: 2, Gap: 2 * time.Second, Settle: 2 * time.Second},
	{Name: "uncacheable-herd", Prof: profile{DelayMS: 200, CacheControl: "private", BodyBytes: 1024}, Keys: 1, Clients: 500, Waves: 1},
	{Name: "error-herd", Prof: profile{DelayMS: 100, Status: 500, BodyBytes: 64}, Keys: 1, Clients: 200, Waves: 3, Gap: time.Second},
	// The same two bursts against an origin that serves 128 requests at once
	// and refuses the rest with 503: the limit that the unbounded origin of
	// the scenarios above never shows.
	{Name: "cold-keys-capped", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 1024, Capacity: 128}, Keys: 1000, Clients: 2, Waves: 1},
	{Name: "sync-expiry-capped", Prof: profile{DelayMS: 20, CacheControl: "max-age=5", BodyBytes: 1024, Capacity: 128}, Keys: 5000, Clients: 1, Prime: 6 * time.Second, Waves: 2, Gap: 2 * time.Second, Settle: 2 * time.Second},
	// A slow origin that still answers: one key, 100 clients, 15 s.
	{Name: "slow-origin-herd", Prof: profile{DelayMS: 15000, CacheControl: "max-age=60", BodyBytes: 1024}, Keys: 1, Clients: 100, Waves: 1},
	// An object larger than a default cache entry (2 MiB), three waves.
	{Name: "oversize-herd", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 2 << 20}, Keys: 1, Clients: 50, Waves: 3, Gap: time.Second},
	{Name: "large-body-herd", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 50 << 20}, Keys: 1, Clients: 20, Waves: 1, Reps: 1},
	{Name: "turned-private", Prof: profile{DelayMS: 200, CacheControl: "max-age=2", BodyBytes: 1024}, Custom: turnedPrivate},
	{Name: "bad-route", Prof: profile{DelayMS: 20, Status: 504, BodyBytes: 64}, Custom: badRoute},
	{Name: "origin-down", Prof: profile{DelayMS: 5, CacheControl: "max-age=2", BodyBytes: 1024}, Custom: originDown},
	{Name: "leader-abort", Prof: profile{DelayMS: 2000, CacheControl: "max-age=60", BodyBytes: 1024}, Custom: leaderAbort},
	{Name: "post-flood", Prof: profile{CacheControl: "max-age=600", BodyBytes: 1024}, Custom: postFlood, Reps: 1},
	{Name: "busting-flood", Prof: profile{DelayMS: 100, CacheControl: "max-age=60", BodyBytes: 1024, Capacity: 128}, Custom: bustingFlood},
	{Name: "brownout", Prof: profile{DelayMS: 5, CacheControl: "max-age=2", BodyBytes: 1024}, Custom: brownout},
	{Name: "purge-herd", Prof: profile{DelayMS: 200, CacheControl: "max-age=600", BodyBytes: 1024}, Custom: purgeHerd},
	{Name: "restart", Prof: profile{DelayMS: 5, CacheControl: "max-age=600", BodyBytes: 1024}, Custom: restart},
}

// row is one line of the results file: one scenario, one repetition.
type row struct {
	Time        string         `json:"time"`
	Subject     string         `json:"subject"`
	Tier        string         `json:"tier"`
	Scenario    string         `json:"scenario"`
	Rep         int            `json:"rep"`
	Requests    int            `json:"requests"`
	Status      map[string]int `json:"status"` // by status code; "error" is a transport failure or timeout
	OriginCalls int64          `json:"origin_calls"`
	OriginPeak  int            `json:"origin_peak"`
	WallMS      float64        `json:"wall_ms"`
	P50MS       float64        `json:"p50_ms"`
	P99MS       float64        `json:"p99_ms"`
	MaxMS       float64        `json:"max_ms"`
	// OriginRefused counts requests a capacity-limited origin turned away.
	OriginRefused int64 `json:"origin_refused,omitempty"`
	// Notes are a scenario's own figures (for example "bystander_failures").
	Notes map[string]float64 `json:"notes,omitempty"`
	// Sent is every request the runner sent to the subject since the last
	// counter reset, and Seen every request the origin counted or refused
	// in that time, over all profiles. With no cache in between the two
	// are equal: the "origin" control row checks the harness in every run.
	Sent int64 `json:"sent"`
	Seen int64 `json:"seen"`
	// NotAvailable, when set, says why the subject cannot run the scenario
	// (no purge, for example). Such a row carries no figures: a feature a
	// cache lacks is never reported as a zero.
	NotAvailable string `json:"not_available,omitempty"`
}

type runner struct {
	client  *http.Client
	target  string // the cache under test
	control string // the origin's control API
	// want is the body length a 2xx response must have, or below zero for
	// no check. cache-handler answered 200 with no body to clients that
	// waited on a failed fetch, and a count of status codes alone called
	// those successes.
	want int64
	sent atomic.Int64
	// purge is "METHOD URL [BODY]", how this subject is told to drop what
	// it stores (subjects/<name>/purge). A URL with "{path}" is sent
	// once per key. Empty: the subject has no purge.
	purge string
	// restart asks whoever started the subject to restart it, and returns
	// when it has. Nil: nothing can.
	restart func(context.Context) error
}

// reply is what one request got back.
type reply struct {
	code string // status code; "error" for a transport failure or timeout; "bad-body:<code>" for a 2xx of the wrong length
	seq  int64  // the origin's X-Origin-Seq: which origin response this is, 0 if absent
}

func (r *runner) ctl(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.control+path, body)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("compare: control %s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// do sends one request to the subject and reads the whole response.
func (r *runner) do(ctx context.Context, method, url string) reply {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return reply{code: "error"}
	}
	r.sent.Add(1)
	resp, err := r.client.Do(req)
	if err != nil {
		return reply{code: "error"}
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return reply{code: "error"}
	}
	out := reply{code: strconv.Itoa(resp.StatusCode)}
	out.seq, _ = strconv.ParseInt(resp.Header.Get("X-Origin-Seq"), 10, 64)
	if resp.StatusCode/100 == 2 && r.want >= 0 && n != r.want && method != http.MethodHead {
		out.code = "bad-body:" + out.code
	}
	return out
}

// get returns the status code as a string, "error", or "bad-body:<code>".
func (r *runner) get(ctx context.Context, url string) string {
	return r.do(ctx, http.MethodGet, url).code
}

// fireAll sends every URL at the same instant.
func (r *runner) fireAll(ctx context.Context, urls []string) ([]reply, []time.Duration) {
	lat := make([]time.Duration, len(urls))
	out := make([]reply, len(urls))
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Go(func() {
			<-gate
			t := time.Now()
			out[i] = r.do(ctx, http.MethodGet, u)
			lat[i] = time.Since(t)
		})
	}
	close(gate)
	wg.Wait()
	return out, lat
}

// fire sends every URL at the same instant and returns the latencies. A nil
// status map discards the codes.
func (r *runner) fire(ctx context.Context, urls []string, status map[string]int) []time.Duration {
	got, lat := r.fireAll(ctx, urls)
	if status != nil {
		for _, g := range got {
			status[g.code]++
		}
	}
	return lat
}

// reset zeroes the origin's counters and the runner's own count of what it
// sent, so the two can be compared at the end.
func (r *runner) reset(ctx context.Context) error {
	r.sent.Store(0)
	return r.ctl(ctx, http.MethodPost, "/reset", nil, nil)
}

// counts reads the origin's counters into the row: the scenario's own
// profile, and the total over every profile beside what the runner sent.
func (r *runner) counts(ctx context.Context, name string, out *row) error {
	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return err
	}
	out.OriginCalls, out.OriginPeak, out.OriginRefused = st[name].Calls, st[name].Peak, st[name].Refused
	out.Sent, out.Seen = r.sent.Load(), 0
	for _, c := range st {
		out.Seen += c.Calls + c.Refused
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// ready polls the origin's "ready" profile through the cache, for 90 s.
func (r *runner) ready(ctx context.Context) error {
	const wait = 90 * time.Second
	deadline := time.Now().Add(wait)
	for !strings.HasSuffix(r.get(ctx, r.target+"/o/ready/x"), "200") { // "ok" is not the scenario's body length
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("compare: %s did not answer 200 within %s", r.target, wait)
		}
		sleep(ctx, 500*time.Millisecond)
	}
	return nil
}

func (r *runner) run(ctx context.Context, sc scenario) (row, error) {
	out := row{Scenario: sc.Name, Status: map[string]int{}}
	// A scenario starts from a cache that answers. One that ended with the
	// origin failing can leave a circuit breaker open, and the next
	// scenario's numbers would then be the breaker's, not its own. The first
	// full run showed exactly that for Weir.
	if err := r.ready(ctx); err != nil {
		return out, err
	}
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, sc.Prof, nil); err != nil {
		return out, err
	}
	r.want = int64(sc.Prof.BodyBytes)
	// A replaced profile keeps its counters, so each repetition zeroes them.
	if err := r.reset(ctx); err != nil {
		return out, err
	}
	// A fresh key space per repetition, so no cache needs a purge or a
	// restart between repetitions.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	if sc.Custom != nil {
		start := time.Now()
		if err := sc.Custom(ctx, r, sc, r.target+"/o/"+sc.Name+"/"+nonce, &out); err != nil {
			return out, err
		}
		out.WallMS = ms(time.Since(start))
		if err := r.counts(ctx, sc.Name, &out); err != nil {
			return out, err
		}
		return out, ctx.Err()
	}
	keys := make([]string, sc.Keys)
	for k := range keys {
		keys[k] = r.target + "/o/" + sc.Name + "/" + nonce + "-" + strconv.Itoa(k)
	}
	if sc.Prime > 0 {
		for chunk := range slices.Chunk(keys, 64) {
			r.fire(ctx, chunk, nil)
		}
		// The counters are zeroed after the wait, not before it: a cache
		// that refreshes ahead of expiry does so during the wait, and those
		// calls belong to priming.
		sleep(ctx, sc.Prime)
		if err := r.reset(ctx); err != nil {
			return out, err
		}
	}
	urls := make([]string, 0, sc.Keys*sc.Clients)
	for range sc.Clients {
		urls = append(urls, keys...)
	}
	var lat []time.Duration
	start := time.Now()
	for w := range sc.Waves {
		if w > 0 {
			sleep(ctx, sc.Gap)
		}
		lat = append(lat, r.fire(ctx, urls, out.Status)...)
		if sc.Settle > 0 && w < sc.Waves-1 {
			if err := r.counts(ctx, sc.Name, &out); err != nil {
				return out, err
			}
			note(&out, "origin_calls_at_end_of_wave_"+strconv.Itoa(w+1), float64(out.OriginCalls))
		}
	}
	out.WallMS = ms(time.Since(start))
	sleep(ctx, sc.Settle)
	if err := r.counts(ctx, sc.Name, &out); err != nil {
		return out, err
	}
	slices.Sort(lat)
	out.Requests = len(lat)
	out.P50MS, out.P99MS, out.MaxMS = ms(lat[len(lat)/2]), ms(lat[len(lat)*99/100]), ms(lat[len(lat)-1])
	return out, ctx.Err()
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func runCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	target := fs.String("target", "", "base URL of the cache under test")
	control := fs.String("control", "", "base URL of the origin's control API")
	subject := fs.String("subject", "", "subject name recorded in each row")
	tier := fs.String("tier", "default", "config tier recorded in each row")
	only := fs.String("scenario", "", "run only these scenarios, comma-separated (default: all)")
	reps := fs.Int("reps", 3, "repetitions per scenario")
	outPath := fs.String("out", "", "append JSON lines to this file (default: stdout)")
	purge := fs.String("purge", "", `how the subject is purged, "METHOD URL [BODY]"; "{path}" in the URL means one request per key (default: no purge)`)
	restartDir := fs.String("restart-dir", "", "directory shared with the driver: the runner writes restart.request there and waits for restart.done (default: no restart)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" || *control == "" || *subject == "" {
		return errors.New("compare: run needs -target, -control and -subject")
	}
	out := io.Writer(os.Stdout)
	if *outPath != "" {
		f, err := os.OpenFile(*outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // the operator names the results file
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	r := &runner{
		// The timeout covers a cache that serializes the uncacheable herd:
		// 500 waiters x 200 ms is 100 s.
		client: &http.Client{Timeout: 180 * time.Second, Transport: &http.Transport{
			MaxIdleConns: 20000, MaxIdleConnsPerHost: 20000, DisableCompression: true,
		}},
		target: strings.TrimRight(*target, "/"), control: strings.TrimRight(*control, "/"),
		want: -1, purge: *purge,
	}
	if *restartDir != "" {
		r.restart = func(ctx context.Context) error { return askDriver(ctx, *restartDir) }
	}
	if err := r.ready(ctx); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	for _, sc := range scenarios {
		if *only != "" && !slices.Contains(strings.Split(*only, ","), sc.Name) {
			continue
		}
		n := *reps
		if sc.Reps > 0 {
			n = min(n, sc.Reps)
		}
		for rep := range n {
			res, err := r.run(ctx, sc)
			if err != nil {
				return err
			}
			res.Time, res.Subject, res.Tier, res.Rep = time.Now().UTC().Format(time.RFC3339), *subject, *tier, rep
			if err := enc.Encode(res); err != nil {
				return err
			}
			log.Printf("%s/%s %s rep %d: %d requests, %d origin calls, peak %d, refused %d, status %v, sent %d, seen %d, notes %v %s",
				*subject, *tier, sc.Name, rep, res.Requests, res.OriginCalls, res.OriginPeak, res.OriginRefused, res.Status, res.Sent, res.Seen, res.Notes, res.NotAvailable)
		}
	}
	return nil
}

// askDriver has run.sh restart the subject: the runner is a container and
// cannot. It writes restart.request into dir and waits for restart.done.
func askDriver(ctx context.Context, dir string) error {
	done := filepath.Join(dir, "restart.done")
	_ = os.Remove(done)
	if err := os.WriteFile(filepath.Join(dir, "restart.request"), nil, 0o644); err != nil { //nolint:gosec // a marker file in the results directory
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if _, err := os.Stat(done); err == nil {
			return os.Remove(done)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return errors.New("compare: the driver did not restart the subject within 2m")
		}
		sleep(ctx, 200*time.Millisecond)
	}
}

func median[T cmp.Ordered](v []T) T {
	slices.Sort(v)
	return v[len(v)/2]
}

// reportCmd prints the median of each scenario's repetitions per subject and
// tier as a Markdown table, in the order the rows were recorded.
func reportCmd(args []string, w io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: compare report <results.jsonl>")
	}
	f, err := os.Open(args[0]) //nolint:gosec // the operator names the results file
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	groups := map[string][]row{}
	var order []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return fmt.Errorf("compare: %s: %w", args[0], err)
		}
		k := r.Scenario + " | " + r.Subject + " | " + r.Tier
		if groups[k] == nil {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	slices.SortStableFunc(order, func(a, b string) int { // group by scenario, keep run order inside it
		return cmp.Compare(strings.SplitN(a, " | ", 2)[0], strings.SplitN(b, " | ", 2)[0])
	})
	_, _ = fmt.Fprintln(w, "| scenario | subject | tier | requests | not 2xx | origin calls | origin peak | p50 ms | p99 ms | wall ms | status of the median repetition | notes |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|")
	violated := 0
	for _, k := range order {
		if na := groups[k][0].NotAvailable; na != "" {
			_, _ = fmt.Fprintf(w, "| %s | not available | | | | | | | | %s |\n", k, na)
			continue
		}
		var calls []int64
		var peak, bad []int
		var p50, p99, wall []float64
		byBad := map[int]string{} // not-2xx count -> that repetition's status codes
		notes := map[string][]float64{}
		for _, r := range groups[k] {
			n := 0
			for code, c := range r.Status {
				if !strings.HasPrefix(code, "2") {
					n += c
				}
			}
			codes := slices.Sorted(maps.Keys(r.Status))
			for i, c := range codes {
				codes[i] = c + ":" + strconv.Itoa(r.Status[c])
			}
			byBad[n] = strings.Join(codes, " ")
			if r.OriginRefused > 0 {
				notes["origin_refused"] = append(notes["origin_refused"], float64(r.OriginRefused))
			}
			for k, v := range r.Notes {
				notes[k] = append(notes[k], v)
			}
			// The control: with no cache, the origin sees what was sent.
			if r.Subject == "origin" && r.Sent != r.Seen {
				violated++
				notes["CONTROL_VIOLATED_sent_minus_seen"] = append(notes["CONTROL_VIOLATED_sent_minus_seen"], float64(r.Sent-r.Seen))
			}
			calls, peak, bad = append(calls, r.OriginCalls), append(peak, r.OriginPeak), append(bad, n)
			p50, p99, wall = append(p50, r.P50MS), append(p99, r.P99MS), append(wall, r.WallMS)
		}
		mb := median(bad)
		var ns []string
		for _, k := range slices.Sorted(maps.Keys(notes)) {
			ns = append(ns, k+"="+strconv.FormatFloat(median(notes[k]), 'f', -1, 64))
		}
		_, _ = fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %.0f | %.0f | %.0f | %s | %s |\n",
			k, groups[k][0].Requests, mb, median(calls), median(peak), median(p50), median(p99), median(wall), byBad[mb], strings.Join(ns, " "))
	}
	if violated > 0 {
		return fmt.Errorf("compare: %d rows of the origin control saw a different number of requests than were sent; no row of this file can be trusted", violated)
	}
	return nil
}
