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
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
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
}

// scenarios are the count-based cases that need no origin failure cue
// (docs/11 §5.1). The ones that do arrive with CMP-03.
var scenarios = []scenario{
	{Name: "cold-herd", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 1024}, Keys: 1, Clients: 1000, Waves: 1},
	{Name: "cold-keys", Prof: profile{DelayMS: 200, CacheControl: "max-age=60", BodyBytes: 1024}, Keys: 1000, Clients: 2, Waves: 1},
	{Name: "sync-expiry", Prof: profile{DelayMS: 20, CacheControl: "max-age=5", BodyBytes: 1024}, Keys: 5000, Clients: 1, Prime: 6 * time.Second, Waves: 1},
	{Name: "uncacheable-herd", Prof: profile{DelayMS: 200, CacheControl: "private", BodyBytes: 1024}, Keys: 1, Clients: 500, Waves: 1},
	{Name: "error-herd", Prof: profile{DelayMS: 100, Status: 500, BodyBytes: 64}, Keys: 1, Clients: 200, Waves: 3, Gap: time.Second},
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
}

type runner struct {
	client  *http.Client
	target  string // the cache under test
	control string // the origin's control API
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

// get returns the status code as a string, or "error".
func (r *runner) get(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "error"
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "error"
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return "error"
	}
	return strconv.Itoa(resp.StatusCode)
}

// fire sends every URL at the same instant and returns the latencies. A nil
// status map discards the codes.
func (r *runner) fire(ctx context.Context, urls []string, status map[string]int) []time.Duration {
	lat := make([]time.Duration, len(urls))
	codes := make([]string, len(urls))
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Go(func() {
			<-gate
			t := time.Now()
			codes[i] = r.get(ctx, u)
			lat[i] = time.Since(t)
		})
	}
	close(gate)
	wg.Wait()
	if status != nil {
		for _, c := range codes {
			status[c]++
		}
	}
	return lat
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// ready polls the origin's "ready" profile through the cache.
func (r *runner) ready(ctx context.Context, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for r.get(ctx, r.target+"/o/ready/x") != "200" {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("compare: %s did not answer 200 within %s", r.target, wait)
		}
		sleep(ctx, 500*time.Millisecond)
	}
	return nil
}

func (r *runner) run(ctx context.Context, sc scenario) (row, error) {
	out := row{Scenario: sc.Name, Status: map[string]int{}}
	if err := r.ctl(ctx, http.MethodPut, "/profile/"+sc.Name, sc.Prof, nil); err != nil {
		return out, err
	}
	// A fresh key space per repetition, so no cache needs a purge or a
	// restart between repetitions.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	keys := make([]string, sc.Keys)
	for k := range keys {
		keys[k] = r.target + "/o/" + sc.Name + "/" + nonce + "-" + strconv.Itoa(k)
	}
	if sc.Prime > 0 {
		for chunk := range slices.Chunk(keys, 64) {
			r.fire(ctx, chunk, nil)
		}
		if err := r.ctl(ctx, http.MethodPost, "/reset", nil, nil); err != nil {
			return out, err
		}
		sleep(ctx, sc.Prime)
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
	}
	out.WallMS = ms(time.Since(start))

	var st map[string]counters
	if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
		return out, err
	}
	out.OriginCalls, out.OriginPeak = st[sc.Name].Calls, st[sc.Name].Peak
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
	only := fs.String("scenario", "", "run one scenario by name (default: all)")
	reps := fs.Int("reps", 3, "repetitions per scenario")
	outPath := fs.String("out", "", "append JSON lines to this file (default: stdout)")
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
	}
	if err := r.ready(ctx, 90*time.Second); err != nil {
		return err
	}
	enc := json.NewEncoder(out)
	for _, sc := range scenarios {
		if *only != "" && sc.Name != *only {
			continue
		}
		for rep := range *reps {
			res, err := r.run(ctx, sc)
			if err != nil {
				return err
			}
			res.Time, res.Subject, res.Tier, res.Rep = time.Now().UTC().Format(time.RFC3339), *subject, *tier, rep
			if err := enc.Encode(res); err != nil {
				return err
			}
			log.Printf("%s/%s %s rep %d: %d requests, %d origin calls, peak %d, status %v, p99 %.0f ms",
				*subject, *tier, sc.Name, rep, res.Requests, res.OriginCalls, res.OriginPeak, res.Status, res.P99MS)
		}
	}
	return nil
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
	_, _ = fmt.Fprintln(w, "| scenario | subject | tier | requests | not 2xx | origin calls | origin peak | p99 ms | wall ms |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---:|---:|---:|---:|---:|---:|")
	for _, k := range order {
		var calls []int64
		var peak, bad []int
		var p99, wall []float64
		for _, r := range groups[k] {
			n := 0
			for code, c := range r.Status {
				if !strings.HasPrefix(code, "2") {
					n += c
				}
			}
			calls, peak, bad = append(calls, r.OriginCalls), append(peak, r.OriginPeak), append(bad, n)
			p99, wall = append(p99, r.P99MS), append(wall, r.WallMS)
		}
		_, _ = fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %.0f | %.0f |\n",
			k, groups[k][0].Requests, median(bad), median(calls), median(peak), median(p99), median(wall))
	}
	return nil
}
