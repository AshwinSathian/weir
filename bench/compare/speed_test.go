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

// CMP-06, docs/11 §6: a rep whose control moved more than 10% is discarded
// and said so; latency is a ratio to the rep's control; CPU, memory per
// object and hit ratio come from the files speed.sh writes.
func TestSpeedReport(t *testing.T) {
	dir := t.TempDir()
	write := func(rep, subject, test string, p50, p99, reqs int64, extra map[string]string) {
		d := filepath.Join(dir, rep, subject)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		r := vegetaReport{Requests: reqs, Success: 1}
		r.Latencies.P50, r.Latencies.P99 = p50, p99
		b, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(d, test+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		for ext, v := range extra {
			if err := os.WriteFile(filepath.Join(d, test+"."+ext), []byte(v), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// rep 1: steady control; rep 2: the control moved 50%.
	write("1", "caddy-nocache", "control-0", 1000, 2000, 100, nil)
	write("1", "caddy-nocache", "control-1", 1050, 2000, 100, nil)
	write("1", "weir", "hit-1024-2000", 500, 1500, 1000, map[string]string{
		"cpu": "100 2100", "hdr": "Value(ms) Percentile TotalCount 1/(1-Percentile)\n0.5 0.99 990 100\n0.9 0.999 999 1000\n",
	})
	write("1", "weir", "fill-100000", 0, 0, 1000, map[string]string{"mem": "1000 501000"})
	write("1", "weir", "zipf", 0, 0, 1000, map[string]string{"origin": "10 210"})
	write("2", "caddy-nocache", "control-0", 1000, 2000, 100, nil)
	write("2", "caddy-nocache", "control-1", 1500, 2000, 100, nil)
	write("2", "weir", "hit-1024-2000", 5000, 9000, 1000, nil)
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
		"rep 2 discarded: control p50 moved 50%",
		"| hit-1024-2000 | weir | 1 | 0.50 | 1.50 | 0.90 | 2.0 | 1.0000 |",
		"| fill-100000 | weir | 1 |", "500 B",
		"| zipf | weir | 1 |", "0.800",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report lacks %q:\n%s", want, got)
		}
	}
}
