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
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
		Max int64 `json:"max"`
	} `json:"latencies"`
}

// measurement is one run of one test against one subject, as speed.sh left
// it in the results directory: <rep>/<subject>/<test>.json (vegeta),
// <test>.hdr (vegeta hdrplot, for p99.9), <test>.cpu (cgroup usage_usec
// before and after), and for fills <test>.mem (memory.current before and
// after).
type measurement struct {
	rep, subject, test string
	report             vegetaReport
	p999               float64 // ms
	cpuUsec            int64
	memBytes           int64
	originCalls        int64
	hasCPU, hasMem     bool
	hasOrigin          bool
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
		m := measurement{rep: parts[0], subject: parts[1], test: strings.TrimSuffix(parts[2], ".json")}
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
		out = append(out, m)
	}
	return out, nil
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
// Percentile, TotalCount, 1/(1-Percentile)) and returns the first value at
// or above p, in ms; NaN when the file is missing.
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
// control between subjects (docs/11 §6).
const controlTest = "control"

// driftLimit is how far a control measurement may move from the rep's
// first before the rep is discarded (docs/11 §6).
const driftLimit = 0.10

// discarded returns the reps whose control p50 moved by more than
// driftLimit, with the reason.
func discarded(ms []measurement) map[string]string {
	first := map[string]float64{}
	out := map[string]string{}
	sorted := slices.Clone(ms)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].subject < sorted[j].subject })
	for _, m := range sorted {
		if !strings.HasPrefix(m.test, controlTest) {
			continue
		}
		p := float64(m.report.Latencies.P50)
		f, ok := first[m.rep]
		if !ok {
			first[m.rep] = p
			continue
		}
		if f > 0 && math.Abs(p-f)/f > driftLimit {
			out[m.rep] = fmt.Sprintf("control p50 moved %.0f%% (%s)", 100*math.Abs(p-f)/f, m.test)
		}
	}
	return out
}

// speedReport prints, per test and subject, medians over the reps that the
// drift gate kept: latency as a ratio to the control of the same rep, CPU
// time per request, success, memory per object and hit ratio.
func speedReport(ms []measurement, w io.Writer) error {
	gone := discarded(ms)
	reps := map[string]bool{}
	for _, m := range ms {
		reps[m.rep] = true
	}
	for rep := range reps {
		if why, ok := gone[rep]; ok {
			_, _ = fmt.Fprintf(w, "rep %s discarded: %s\n", rep, why)
		}
	}
	control := map[string]float64{} // rep -> control p50 (ns), the first one of the rep
	for _, m := range ms {
		if m.test == controlTest+"-0" {
			control[m.rep] = float64(m.report.Latencies.P50)
		}
	}
	type key struct{ test, subject string }
	groups := map[key][]measurement{}
	for _, m := range ms {
		if _, bad := gone[m.rep]; bad || strings.HasPrefix(m.test, controlTest) || strings.HasPrefix(m.test, "warm-") {
			continue
		}
		groups[key{m.test, m.subject}] = append(groups[key{m.test, m.subject}], m)
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].test != keys[j].test {
			return keys[i].test < keys[j].test
		}
		return keys[i].subject < keys[j].subject
	})
	_, _ = fmt.Fprintln(w, "| test | subject | reps | p50 / control | p99 / control | p99.9 ms | CPU µs/request | success | memory/object | hit ratio |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|")
	for _, k := range keys {
		g := groups[k]
		var r50, r99, p999, cpu, succ, mem, hit []float64
		for _, m := range g {
			if c := control[m.rep]; c > 0 {
				r50 = append(r50, float64(m.report.Latencies.P50)/c)
				r99 = append(r99, float64(m.report.Latencies.P99)/c)
			}
			if !math.IsNaN(m.p999) {
				p999 = append(p999, m.p999)
			}
			if m.hasCPU && m.report.Requests > 0 {
				cpu = append(cpu, float64(m.cpuUsec)/float64(m.report.Requests))
			}
			succ = append(succ, m.report.Success)
			if m.hasMem && m.report.Requests > 0 {
				mem = append(mem, float64(m.memBytes)/float64(m.report.Requests))
			}
			if m.hasOrigin && m.report.Requests > 0 {
				hit = append(hit, 1-float64(m.originCalls)/float64(m.report.Requests))
			}
		}
		_, _ = fmt.Fprintf(w, "| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s |\n", k.test, k.subject, len(g),
			cell(r50, "%.2f"), cell(r99, "%.2f"), cell(p999, "%.2f"), cell(cpu, "%.1f"), cell(succ, "%.4f"), bytesCell(mem), cell(hit, "%.3f"))
	}
	return nil
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
