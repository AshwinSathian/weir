package main

// The speed and memory pillar (docs/11 §5.5, §6). The load comes from
// vegeta, an established open-loop generator (docs/11 §4); this file only
// writes its target lists and reads its reports back. speed.sh drives it.

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// targetsCmd prints vegeta targets ("GET <url>" lines) for one workload:
//
//	uniform: -n lines over -keys keys, each key equally likely
//	zipf:    the same with a Zipf(-s) popularity, key 0 the most popular
//	scan:    zipf, with every -scan-every'th line a key never asked before
//	fill:    each of -keys keys once, in order
//
// -seed makes a list repeatable, so every subject gets the same one.
func targetsCmd(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("targets", flag.ExitOnError)
	base := fs.String("base", "", "URL prefix, for example http://varnish:8080/o/speed/")
	mode := fs.String("mode", "uniform", "uniform, zipf, scan or fill")
	keys := fs.Int("keys", 1000, "distinct keys")
	n := fs.Int("n", 100000, "lines (not for fill)")
	s := fs.Float64("s", 1.1, "Zipf exponent, above 1")
	scanEvery := fs.Int("scan-every", 5, "scan: one never-seen key per this many lines")
	seed := fs.Uint64("seed", 1, "random seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || *keys < 1 || *n < 1 {
		return errors.New("targets: -base, -keys >= 1 and -n >= 1 are required")
	}
	bw := bufio.NewWriter(w)
	line := func(k string) { _, _ = fmt.Fprintf(bw, "GET %s%s\n", *base, k) }
	r := rand.New(rand.NewPCG(*seed, *seed^0x9e3779b97f4a7c15)) //nolint:gosec // repeatable workloads, not secrets
	switch *mode {
	case "fill":
		for i := range *keys {
			line(strconv.Itoa(i))
		}
	case "uniform":
		for range *n {
			line(strconv.Itoa(r.IntN(*keys)))
		}
	case "zipf", "scan":
		if *s <= 1 {
			return errors.New("targets: -s must be above 1")
		}
		z := rand.NewZipf(r, *s, 1, uint64(*keys-1))
		next := *keys // scan keys start after the Zipf key space
		for i := range *n {
			if *mode == "scan" && *scanEvery > 0 && i%*scanEvery == 0 {
				line(strconv.Itoa(next))
				next++
				continue
			}
			line(strconv.FormatUint(z.Uint64(), 10))
		}
	default:
		return fmt.Errorf("targets: unknown mode %q", *mode)
	}
	return bw.Flush()
}

// vegetaReport is the part of `vegeta report -type=json` the pillar reads.
type vegetaReport struct {
	Requests  int64   `json:"requests"`
	Rate      float64 `json:"rate"`
	Success   float64 `json:"success"`
	Latencies struct {
		P50 int64 `json:"50th"`
		P99 int64 `json:"99th"`
	} `json:"latencies"`
	BytesIn struct {
		Mean float64 `json:"mean"`
	} `json:"bytes_in"`
}

// measurement is one run of one test against one subject, as speed.sh left
// it in the results directory: <rep>/<subject>/<test>.json (vegeta), and
// beside it .hdr (vegeta's hdrplot quantiles, for p99.9), .cpu (cgroup
// usage_usec before and after), .mem (memory.stat anon before and after),
// .origin (origin calls before and after), .rate (the rate asked for) and
// .size (the body size every response must have).
type measurement struct {
	rep, subject, test string
	control            int // the control measured just before this subject
	report             vegetaReport
	p999               float64 // ms
	cpuUsec            int64
	memBytes           int64
	originCalls        int64
	rate, size         int64
	hasCPU, hasMem     bool
	hasOrigin          bool
}

// minP999Requests is the fewest requests whose p99.9 the report prints: below
// it the 99.9th percentile is one of the last few samples.
const minP999Requests = 10000

// countTest reports a test whose figures are counts (memory, hit ratio), which
// docs/11 §6 publishes as measured, outside the drift gate.
func countTest(test string) bool {
	return strings.HasPrefix(test, "fill-") || test == "zipf" || test == "scan"
}

// badBody reports a run whose responses were not all the size the origin
// sends: an empty or cut 200 is a failure, not a fast success (docs/11 §4).
func (m measurement) badBody() bool {
	return m.size > 0 && m.report.Requests > 0 && m.report.BytesIn.Mean != float64(m.size)
}

// sustained reports a run the subject kept up with: 99.9% good responses of
// the right size at 95% or more of the rate asked for.
func (m measurement) sustained() bool {
	return m.report.Success >= 0.999 && !m.badBody() && (m.rate == 0 || m.report.Rate >= 0.95*float64(m.rate))
}

// speedCmd prints the pillar's report from a speed.sh results directory.
func speedCmd(args []string, w io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: compare speed <results/speed-STAMP>")
	}
	ms, err := loadMeasurements(args[0])
	if err != nil {
		return err
	}
	return speedReport(ms, w)
}

func loadMeasurements(dir string) ([]measurement, error) {
	var out []measurement
	files, err := filepath.Glob(filepath.Join(dir, "*", "*", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f)
		parts := strings.Split(rel, string(filepath.Separator))
		m := measurement{rep: parts[0], subject: parts[1], test: strings.TrimSuffix(parts[2], ".json"), control: -1}
		b, err := os.ReadFile(f) //nolint:gosec // the operator names the directory
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m.report); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		stem := strings.TrimSuffix(f, ".json")
		m.p999 = hdrPercentile(stem+".hdr", 0.999)
		if a, b, ok := twoInts(stem + ".cpu"); ok {
			m.cpuUsec, m.hasCPU = b-a, true
		}
		if a, b, ok := twoInts(stem + ".mem"); ok {
			m.memBytes, m.hasMem = b-a, true
		}
		if a, b, ok := twoInts(stem + ".origin"); ok {
			m.originCalls, m.hasOrigin = b-a, true
		}
		m.rate, _ = oneInt(stem + ".rate")
		m.size, _ = oneInt(stem + ".size")
		if c, ok := oneInt(filepath.Join(filepath.Dir(f), "control.idx")); ok {
			m.control = int(c)
		}
		out = append(out, m)
	}
	return out, nil
}

// oneInt reads a file holding one integer.
func oneInt(path string) (int64, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // inside the operator's results directory
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

// twoInts reads a file holding two integers (a before and an after).
func twoInts(path string) (int64, int64, bool) {
	b, err := os.ReadFile(path) //nolint:gosec // inside the operator's results directory
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.ParseInt(f[0], 10, 64)
	c, err2 := strconv.ParseInt(f[1], 10, 64)
	return a, c, err1 == nil && err2 == nil
}

// hdrPercentile reads `vegeta report -type=hdrplot` output (columns Value(ms),
// Percentile, TotalCount, 1/(1-Percentile); vegeta computes the quantiles
// with a t-digest) and returns the first value at or above p, in ms; NaN when
// the file is missing.
func hdrPercentile(path string, p float64) float64 {
	b, err := os.ReadFile(path) //nolint:gosec // inside the operator's results directory
	if err != nil {
		return math.NaN()
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, err1 := strconv.ParseFloat(f[0], 64)
		q, err2 := strconv.ParseFloat(f[1], 64)
		if err1 == nil && err2 == nil && q >= p {
			return v
		}
	}
	return math.NaN()
}

// controlTest is the measurement every rep repeats on the caddy-nocache
// control between subjects (docs/11 §6): control-0, control-1, ...
const controlTest = "control-"

// driftLimit is how far a control measurement may move from the rep's
// first before the rep's timing rows are discarded (docs/11 §6).
const driftLimit = 0.10

// controls returns each rep's control measurements by index.
func controls(ms []measurement) map[string]map[int]measurement {
	out := map[string]map[int]measurement{}
	for _, m := range ms {
		n, err := strconv.Atoi(strings.TrimPrefix(m.test, controlTest))
		if !strings.HasPrefix(m.test, controlTest) || err != nil {
			continue
		}
		if out[m.rep] == nil {
			out[m.rep] = map[int]measurement{}
		}
		out[m.rep][n] = m
	}
	return out
}

// discarded returns the reps whose control p50 or p99 moved by more than
// driftLimit from the rep's first control, with the reason.
func discarded(ms []measurement) map[string]string {
	out := map[string]string{}
	for rep, cs := range controls(ms) {
		first, ok := cs[0]
		if !ok {
			continue
		}
		idx := slices.Sorted(maps.Keys(cs))
		for _, i := range idx {
			c := cs[i]
			for _, q := range []struct {
				name     string
				got, was int64
			}{{"p50", c.report.Latencies.P50, first.report.Latencies.P50}, {"p99", c.report.Latencies.P99, first.report.Latencies.P99}} {
				if q.was > 0 && math.Abs(float64(q.got-q.was))/float64(q.was) > driftLimit {
					if _, seen := out[rep]; !seen {
						out[rep] = fmt.Sprintf("control %s moved %.0f%% (control-%d)", q.name, 100*math.Abs(float64(q.got-q.was))/float64(q.was), i)
					}
				}
			}
		}
	}
	return out
}

// speedReport prints, per test and subject, medians over the reps that the
// drift gate kept: latency as a ratio to the control measured just before
// the subject (p50 to its p50, p99 to its p99, p99.9 to its p99.9), CPU
// time per request, success, memory per object and hit ratio; then the
// highest rate each subject sustained per body size.
func speedReport(ms []measurement, w io.Writer) error {
	gone := discarded(ms)
	for _, rep := range slices.Sorted(maps.Keys(gone)) {
		_, _ = fmt.Fprintf(w, "rep %s: timing rows discarded, %s\n", rep, gone[rep])
	}
	cs := controls(ms)
	type key struct{ test, subject string }
	groups := map[key][]measurement{}
	for _, m := range ms {
		if strings.HasPrefix(m.test, controlTest) || strings.HasPrefix(m.test, "warm") {
			continue
		}
		if _, bad := gone[m.rep]; bad && !countTest(m.test) {
			continue
		}
		groups[key{m.test, m.subject}] = append(groups[key{m.test, m.subject}], m)
	}
	keys := slices.SortedFunc(maps.Keys(groups), func(a, b key) int {
		if c := strings.Compare(a.test, b.test); c != 0 {
			return c
		}
		return strings.Compare(a.subject, b.subject)
	})
	_, _ = fmt.Fprintln(w, "| test | subject | reps | p50 / control | p99 / control | p99.9 / control | CPU µs/request | success | origin calls | memory/object | hit ratio |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|---|")
	for _, k := range keys {
		g := groups[k]
		var r50, r99, r999, cpu, succ, mem, hit, calls []float64
		bad := 0
		for _, m := range g {
			if m.badBody() {
				bad++
				continue
			}
			if c, ok := cs[m.rep][m.control]; ok && !countTest(m.test) {
				if c.report.Latencies.P50 > 0 {
					r50 = append(r50, float64(m.report.Latencies.P50)/float64(c.report.Latencies.P50))
				}
				if c.report.Latencies.P99 > 0 {
					r99 = append(r99, float64(m.report.Latencies.P99)/float64(c.report.Latencies.P99))
				}
				if m.report.Requests >= minP999Requests && !math.IsNaN(m.p999) && c.p999 > 0 {
					r999 = append(r999, m.p999/c.p999)
				}
			}
			if m.hasCPU && m.report.Requests > 0 {
				cpu = append(cpu, float64(m.cpuUsec)/float64(m.report.Requests))
			}
			succ = append(succ, m.report.Success)
			if m.hasMem && m.report.Requests > 0 {
				mem = append(mem, float64(m.memBytes)/float64(m.report.Requests))
			}
			if m.hasOrigin && m.report.Requests > 0 {
				calls = append(calls, float64(m.originCalls))
				if countTest(m.test) {
					hit = append(hit, 1-float64(m.originCalls)/float64(m.report.Requests))
				}
			}
		}
		succCell := cell(succ, "%.4f")
		if bad > 0 {
			succCell = fmt.Sprintf("bad body in %d of %d", bad, len(g))
		}
		_, _ = fmt.Fprintf(w, "| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s |\n", k.test, k.subject, len(g),
			cell(r50, "%.2f"), cell(r99, "%.2f"), cell(r999, "%.2f"), cell(cpu, "%.1f"), succCell, cell(calls, "%.0f"), bytesCell(mem), cell(hit, "%.3f"))
	}
	saturation(ms, gone, w)
	return nil
}

// saturation prints, per subject and body size, the highest rate the subject
// sustained in each kept rep (median over reps).
func saturation(ms []measurement, gone map[string]string, w io.Writer) {
	type key struct {
		subject string
		size    int64
	}
	best := map[key]map[string]int64{} // -> rep -> highest sustained rate
	for _, m := range ms {
		timed := strings.HasPrefix(m.test, "hit-") || strings.HasPrefix(m.test, "sat-")
		if _, bad := gone[m.rep]; bad || m.rate == 0 || !timed || !m.sustained() {
			continue
		}
		k := key{m.subject, m.size}
		if best[k] == nil {
			best[k] = map[string]int64{}
		}
		best[k][m.rep] = max(best[k][m.rep], m.rate)
	}
	if len(best) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\n| subject | body bytes | highest rate sustained (requests/s) |")
	_, _ = fmt.Fprintln(w, "|---|---|---|")
	ks := slices.SortedFunc(maps.Keys(best), func(a, b key) int {
		if c := strings.Compare(a.subject, b.subject); c != 0 {
			return c
		}
		return int(a.size - b.size)
	})
	for _, k := range ks {
		var v []float64
		for _, r := range best[k] {
			v = append(v, float64(r))
		}
		_, _ = fmt.Fprintf(w, "| %s | %d | %s |\n", k.subject, k.size, cell(v, "%.0f"))
	}
}

func cell(v []float64, f string) string {
	if len(v) == 0 {
		return "not available"
	}
	return fmt.Sprintf(f, median(slices.Clone(v)))
}

func bytesCell(v []float64) string {
	if len(v) == 0 {
		return "not available"
	}
	return strconv.FormatFloat(median(slices.Clone(v)), 'f', 0, 64) + " B"
}
