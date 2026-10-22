package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CMP-06, docs/11 §5.5: the target lists have the shape each test needs,
// and the same seed gives the same list for every subject.
func TestTargets(t *testing.T) {
	run := func(args ...string) []string {
		var b bytes.Buffer
		if err := targetsCmd(append([]string{"-base", "http://s:8080/o/p/"}, args...), &b); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(b.String()), "\n")
	}
	if l := run("-mode", "fill", "-keys", "5"); len(l) != 5 || l[4] != "GET http://s:8080/o/p/4" {
		t.Fatalf("fill: %v", l)
	}
	z := run("-mode", "zipf", "-keys", "1000", "-n", "20000")
	if len(z) != 20000 {
		t.Fatalf("zipf: %d lines", len(z))
	}
	hot := 0
	for _, l := range z {
		if l == "GET http://s:8080/o/p/0" {
			hot++
		}
	}
	if hot < 20000/10 {
		t.Fatalf("zipf: key 0 asked %d times of 20000, want the most popular by far", hot)
	}
	if again := run("-mode", "zipf", "-keys", "1000", "-n", "20000"); strings.Join(again, "") != strings.Join(z, "") {
		t.Fatal("the same seed gave another list")
	}
	scan := run("-mode", "scan", "-keys", "1000", "-n", "100", "-scan-every", "5")
	fresh := 0
	for _, l := range scan {
		k, _ := strconv.Atoi(strings.TrimPrefix(l, "GET http://s:8080/o/p/"))
		if k >= 1000 {
			fresh++
		}
	}
	if fresh != 20 {
		t.Fatalf("scan: %d never-seen keys in 100 lines, want 20", fresh)
	}
}

// CMP-06, docs/11 §6: a rep whose control moved more than 10% loses its
// timing rows and the report says so, while its counts stay; latency is a
// ratio to the control measured just before the subject, percentile to
// percentile; a wrong body size is a failure; CPU, origin calls, memory per
// object, hit ratio and the highest sustained rate come from the files
// speed.sh writes.
func TestSpeedReport(t *testing.T) {
	dir := t.TempDir()
	write := func(rep, subject, test string, p50, p99, reqs int64, bytesIn float64, rate float64, extra map[string]string) {
		d := filepath.Join(dir, rep, subject)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		r := vegetaReport{Requests: reqs, Success: 1, Rate: rate}
		r.Latencies.P50, r.Latencies.P99 = p50, p99
		r.BytesIn.Total = int64(bytesIn * float64(reqs))
		b, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(d, test+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		for name, v := range extra {
			p := filepath.Join(d, test+"."+name)
			if name == "control.idx" {
				p = filepath.Join(d, name)
			}
			if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	hdr := func(p999 string) string {
		return "Value(ms) Percentile TotalCount 1/(1-Percentile)\n0.5 0.99 990 100\n" + p999 + " 0.999 999 1000\n"
	}
	// rep 1: controls steady; the subject follows control-1.
	write("1", "caddy-nocache", "control-0", 1000, 2000, 20000, 1024, 2000, map[string]string{"hdr": hdr("4")})
	write("1", "caddy-nocache", "control-1", 1050, 2100, 20000, 1024, 2000, map[string]string{"hdr": hdr("5")})
	write("1", "weir", "hit-1024-2000", 525, 1050, 20000, 1024, 2000, map[string]string{
		"cpu": "100 40100", "hdr": hdr("2.5"), "origin": "7 7", "rate": "2000", "size": "1024", "control.idx": "1",
	})
	write("1", "weir", "sat-1024-12000", 600, 1500, 120000, 1024, 11900, map[string]string{"rate": "12000", "size": "1024"})
	write("1", "weir", "sat-1024-18000", 600, 9000, 180000, 1024, 15000, map[string]string{"rate": "18000", "size": "1024"}) // fell behind
	write("1", "weir", "fill-100000", 0, 0, 1000, 100, 0, map[string]string{"mem": "1000 501000", "size": "100"})
	write("1", "weir", "zipf", 0, 0, 1000, 3000, 0, map[string]string{"origin": "10 210", "size": "3000"})
	write("1", "souin", "hit-1024-2000", 100, 200, 20000, 0, 2000, map[string]string{"size": "1024", "rate": "2000", "control.idx": "1"})
	// rep 2: the control's p99 moved 50%: timing rows go, the fill count stays.
	write("2", "caddy-nocache", "control-0", 1000, 2000, 20000, 1024, 2000, nil)
	write("2", "caddy-nocache", "control-1", 1000, 3000, 20000, 1024, 2000, nil)
	write("2", "weir", "hit-1024-2000", 5000, 9000, 20000, 1024, 2000, map[string]string{"size": "1024", "rate": "2000", "control.idx": "1"})
	write("2", "weir", "fill-100000", 0, 0, 1000, 100, 0, map[string]string{"mem": "0 700000", "size": "100"})
	ms, err := loadMeasurements(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := speedReport(ms, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"rep 2: timing rows discarded, control p99 moved 50% (control-1)",
		"| hit-1024-2000 | weir | 1 | 0.50 | 0.50 | 0.50 | 2.0 | 1.0000 | 0 |",
		"| hit-1024-2000 | souin | 1 | not available | not available | not available | not available | bad body in 1 of 1 |",
		"| fill-100000 | weir | 2 |", "700 B",
		"| zipf | weir | 1 |", "0.800",
		"| weir | 1024 | 12000 |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report lacks %q:\n%s", want, got)
		}
	}
}

// CMP-06, docs/11 §4: errors are not bad bodies (a timeout carries no body
// at all); a climb that never failed reports its top rate as a lower bound.
func TestSpeedReportErrorsAndLowerBound(t *testing.T) {
	dir := t.TempDir()
	put := func(rep, subject, test string, reqs int64, success float64, total int64, p99 int64, rate int64, size int64, ctrl string) {
		d := filepath.Join(dir, rep, subject)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		r := vegetaReport{Requests: reqs, Success: success, Rate: float64(rate)}
		r.Latencies.P50, r.Latencies.P99 = 100, p99
		r.BytesIn.Total = total
		b, _ := json.Marshal(r)
		files := map[string]string{test + ".json": string(b)}
		if size > 0 {
			files[test+".size"], files[test+".rate"] = strconv.FormatInt(size, 10), strconv.FormatInt(rate, 10)
		}
		if ctrl != "" {
			files["control.idx"] = ctrl
		}
		for n, v := range files {
			if err := os.WriteFile(filepath.Join(d, n), []byte(v), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	put("1", "caddy-nocache", "control-0", 1000, 1, 1024000, 200, 2000, 0, "")
	// 1% timeouts: 19 800 successes of 1024 bytes, no bad body, not sustained.
	put("1", "varnish", "hit-1024-2000", 20000, 0.99, 19800*1024, 300, 2000, 1024, "0")
	// weir climbed to its last step without failing.
	put("1", "weir", "hit-1024-2000", 20000, 1, 20000*1024, 300, 2000, 1024, "0")
	put("1", "weir", "sat-1024-12000", 120000, 1, 120000*1024, 900, 12000, 1024, "0")
	ms, err := loadMeasurements(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := speedReport(ms, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "bad body") {
		t.Fatalf("timeouts reported as bad bodies:\n%s", got)
	}
	for _, want := range []string{"| hit-1024-2000 | varnish | 1 |", "0.9900", "| weir | 1024 | ≥ 12000 |"} {
		if !strings.Contains(got, want) {
			t.Fatalf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| varnish | 1024 |") {
		t.Fatalf("a run with 1%% errors counted as sustained:\n%s", got)
	}
}
