package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newRunner(t *testing.T) *runner {
	t.Helper()
	o := newOrigin()
	data := httptest.NewServer(o.data())
	ctl := httptest.NewServer(o.control())
	t.Cleanup(data.Close)
	t.Cleanup(ctl.Close)
	return &runner{client: http.DefaultClient, target: data.URL, control: ctl.URL}
}

// With no cache in between, every request is an origin call: the runner's
// counts and the origin's must agree exactly (docs/11 §4).
func TestRunCountsAgainstOriginDirect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sc        scenario
		requests  int
		status    string
		wantCalls int64
	}{
		{"every wave sends keys x clients", scenario{Name: "a", Keys: 3, Clients: 4, Waves: 2}, 24, "200", 24},
		{"priming is not counted", scenario{Name: "b", Keys: 5, Clients: 1, Prime: time.Nanosecond, Waves: 1}, 5, "200", 5},
		{"the profile's status reaches the client", scenario{Name: "c", Prof: profile{Status: 500}, Keys: 1, Clients: 7, Waves: 1}, 7, "500", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRunner(t)
			if err := r.ready(t.Context(), time.Second); err != nil {
				t.Fatal(err)
			}
			got, err := r.run(t.Context(), tc.sc)
			if err != nil {
				t.Fatal(err)
			}
			if got.Requests != tc.requests || got.Status[tc.status] != tc.requests || got.OriginCalls != tc.wantCalls {
				t.Fatalf("got %d requests, status %v, %d origin calls; want %d, all %s, %d",
					got.Requests, got.Status, got.OriginCalls, tc.requests, tc.status, tc.wantCalls)
			}
			if got.OriginPeak < 1 || got.OriginPeak > tc.sc.Keys*tc.sc.Clients {
				t.Fatalf("origin peak %d outside 1..%d", got.OriginPeak, tc.sc.Keys*tc.sc.Clients)
			}
		})
	}
}

func TestOriginControl(t *testing.T) {
	r := newRunner(t)
	if got := r.get(t.Context(), r.target+"/o/nope/x"); got != "404" {
		t.Fatalf("unknown profile answered %s, want 404", got)
	}
	for name, p := range map[string]profile{
		"status below 200": {Status: 99},
		"negative delay":   {DelayMS: -1},
		"body over bound":  {BodyBytes: maxBodyBytes + 1},
	} {
		if err := r.ctl(t.Context(), http.MethodPut, "/profile/bad", p, nil); err == nil {
			t.Errorf("%s: profile accepted", name)
		}
	}
	// The profile table is bounded: "ready" plus maxProfiles-1 more.
	for i := range maxProfiles {
		err := r.ctl(t.Context(), http.MethodPut, "/profile/p"+strconv.Itoa(i), profile{}, nil)
		if full := i >= maxProfiles-1; (err != nil) != full {
			t.Fatalf("profile %d: err %v, want refused=%v", i, err, full)
		}
	}
}

func TestReportMedians(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i, calls := range []int64{9, 1, 2} {
		_ = enc.Encode(row{Subject: "s", Tier: "default", Scenario: "cold-herd", Rep: i, Requests: 10,
			Status: map[string]int{"200": 9, "error": 1}, OriginCalls: calls, OriginPeak: 3})
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := reportCmd([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "| cold-herd | s | default | 10 | 1 | 2 | 3 | 0 | 0 |"; !strings.Contains(out.String(), want) {
		t.Fatalf("report:\n%s\nwant a line %q", out.String(), want)
	}
}
