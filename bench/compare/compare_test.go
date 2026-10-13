package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// memTransport serves requests by calling a handler directly, chosen by the
// URL's host. No socket is involved, so a test can run inside a synctest
// bubble and a scenario's seconds of waiting cost nothing.
type memTransport map[string]http.Handler

type memWriter struct {
	h     http.Header
	pw    *io.PipeWriter
	once  sync.Once
	code  int
	ready chan struct{}
}

func (w *memWriter) Header() http.Header { return w.h }
func (w *memWriter) WriteHeader(code int) {
	w.once.Do(func() { w.code = code; close(w.ready) })
}

func (w *memWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.pw.Write(b)
}

func (m memTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	w := &memWriter{h: http.Header{}, pw: pw, ready: make(chan struct{})}
	in := req.Clone(req.Context())
	in.RequestURI = req.URL.RequestURI()
	if in.Body == nil {
		in.Body = http.NoBody
	}
	go func() {
		m[req.URL.Host].ServeHTTP(w, in)
		w.WriteHeader(http.StatusOK)
		_ = pw.Close()
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		_ = pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: w.code, Status: http.StatusText(w.code), Header: w.h.Clone(), Body: pr, ContentLength: -1, Request: req}, nil
}

// direct is a runner wired to the origin with nothing in between.
func direct() (*runner, *origin, memTransport) {
	o := newOrigin()
	m := memTransport{"subject": o.data(), "control": o.control()}
	return &runner{client: &http.Client{Transport: m}, target: "http://subject", control: "http://control", want: -1,
		restart: func(context.Context) error { return nil }}, o, m
}

// With no cache in between, every request the runner sends is one the origin
// counts or refuses (docs/11 §4). Every scenario is held to that, so a
// scenario that sends requests it does not account for, or reads the
// counters at the wrong moment, fails here before it measures a cache.
func TestRunCountsAgainstOriginDirect(t *testing.T) {
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			if testing.Short() && (sc.Name == "post-flood" || sc.Name == "large-body-herd") {
				t.Skip("300 000 requests or 1 GiB of bodies")
			}
			synctest.Test(t, func(t *testing.T) {
				r, _, _ := direct()
				got, err := r.run(t.Context(), sc)
				if err != nil {
					t.Fatal(err)
				}
				if got.NotAvailable != "" {
					if sc.Name != "purge-herd" {
						t.Fatalf("not available: %s", got.NotAvailable)
					}
					return // the origin has no purge
				}
				if got.Sent == 0 || got.Sent != got.Seen {
					t.Fatalf("runner sent %d requests, origin saw %d", got.Sent, got.Seen)
				}
				if got.Requests == 0 || got.Status["error"] != 0 {
					t.Fatalf("%d requests, status %v", got.Requests, got.Status)
				}
				for code := range got.Status {
					if strings.HasPrefix(code, "bad-body") {
						t.Fatalf("the origin's own bodies were judged wrong: %v", got.Status)
					}
				}
				if sc.Custom == nil && got.OriginCalls+got.OriginRefused != int64(got.Requests) {
					t.Fatalf("%d requests in the row, %d origin calls and %d refusals", got.Requests, got.OriginCalls, got.OriginRefused)
				}
			})
		})
	}
}

// What each new scenario reports when nothing caches: the figures a cache's
// row is read against.
func TestScenarioFiguresAgainstOriginDirect(t *testing.T) {
	run := func(t *testing.T, name string) row {
		t.Helper()
		r, _, _ := direct()
		for _, sc := range scenarios {
			if sc.Name == name {
				got, err := r.run(t.Context(), sc)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
		}
		t.Fatalf("no scenario %q", name)
		return row{}
	}
	t.Run("brownout fails every request of a burst and none outside", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			got := run(t, "brownout")
			n := got.Notes
			if got.Requests != 12000 || n["burst_requests"] != 4000 || n["burst_failures"] != 4000 || n["failures_outside_bursts"] != 0 ||
				n["origin_calls_in_bursts"] != 4000 || n["last_failure_s_after_recovery"] != 0 {
				t.Fatalf("%d requests, notes %v", got.Requests, n)
			}
		})
	})
	t.Run("busting flood overruns the origin and bystanders fail with it", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			got := run(t, "busting-flood")
			n := got.Notes
			if got.Requests != 1000 || n["flood_sent"] != 10000 || n["bystander_failures"] == 0 || n["flood_failures"] == 0 ||
				n["bystander_origin_calls"]+n["bystander_origin_refused"] != 1000 || n["bystander_origin_refused"] != n["bystander_failures"] {
				t.Fatalf("%d requests, notes %v", got.Requests, n)
			}
			if got.OriginPeak > 128 {
				t.Fatalf("origin peak %d over its capacity of 128", got.OriginPeak)
			}
		})
	})
	t.Run("restart replays every key at the origin", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			if got := run(t, "restart"); got.Requests != 2000 || got.OriginCalls != 2000 {
				t.Fatalf("%d requests, %d origin calls", got.Requests, got.OriginCalls)
			}
		})
	})
	t.Run("sync-expiry counts both waves and nothing from priming", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			got := run(t, "sync-expiry")
			if got.Requests != 10000 || got.OriginCalls != 10000 || got.Notes["origin_calls_at_end_of_wave_1"] != 5000 {
				t.Fatalf("%d requests, %d origin calls, notes %v", got.Requests, got.OriginCalls, got.Notes)
			}
		})
	})
	t.Run("post-flood records what the flood was answered", func(t *testing.T) {
		if testing.Short() {
			t.Skip("300 000 requests")
		}
		synctest.Test(t, func(t *testing.T) {
			got := run(t, "post-flood")
			if n := got.Notes; n["post_2xx"] != 300000 || n["post_not_2xx"] != 0 || n["entries_lost"] != 2000 {
				t.Fatalf("notes %v", n)
			}
		})
	})
}

// memCache stores every 200 by path and serves it until told to PURGE the
// path. With deaf set it answers the purge and keeps the entry.
func memCache(origin http.Handler, deaf bool) http.Handler {
	type entry struct {
		h    http.Header
		body []byte
	}
	var mu sync.Mutex
	m := map[string]entry{}
	c := &http.Client{Transport: memTransport{"o": origin}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		e, ok := m[r.URL.Path]
		if r.Method == "PURGE" {
			if !deaf {
				delete(m, r.URL.Path)
			}
			mu.Unlock()
			return
		}
		mu.Unlock()
		if !ok {
			req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://o"+r.URL.RequestURI(), nil)
			resp, err := c.Do(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			e = entry{resp.Header, b}
			if resp.StatusCode == http.StatusOK && r.URL.Path != "/o/ready/x" {
				mu.Lock()
				m[r.URL.Path] = e
				mu.Unlock()
			}
		}
		for k, v := range e.h {
			w.Header()[k] = v
		}
		_, _ = w.Write(e.body)
	})
}

// purge-herd must tell a cache that purged from one that only said so.
func TestPurgeHerdSeesWhetherThePurgeWorked(t *testing.T) {
	for name, tc := range map[string]struct {
		deaf      bool
		wantCalls int64
		wantStale float64
	}{
		"a cache that purges refetches every key once": {false, 1000, 0},
		"a cache that ignores the purge is reported":   {true, 0, 1000},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r, o, m := direct()
				m["subject"] = memCache(o.data(), tc.deaf)
				r.purge = "PURGE http://subject{path}"
				var sc scenario
				for _, s := range scenarios {
					if s.Name == "purge-herd" {
						sc = s
					}
				}
				got, err := r.run(t.Context(), sc)
				if err != nil {
					t.Fatal(err)
				}
				n := got.Notes
				if got.Requests != 2000 || got.OriginCalls != tc.wantCalls || n["origin_calls_before_purge"] != 0 || n["not_refreshed_at_last_read"] != tc.wantStale {
					t.Fatalf("%d requests, %d origin calls, notes %v", got.Requests, got.OriginCalls, n)
				}
			})
		})
	}
}

// A 2xx whose body is not the profile's length is a failure, not a success:
// what cache-handler sent to the waiters of a failed fetch (docs/11 §10).
func TestEmptySuccessIsAFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, m := direct()
		m["subject"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/o/ready/") {
				_, _ = io.WriteString(w, "ok")
			}
			// everything else: 200, no body, and the origin is never asked
		})
		got, err := r.run(t.Context(), scenario{Name: "empty", Prof: profile{BodyBytes: 64}, Keys: 1, Clients: 5, Waves: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got.Status["bad-body:200"] != 5 || got.Status["200"] != 0 {
			t.Fatalf("status %v, want five bad-body:200", got.Status)
		}
	})
}

// The origin's cues, each against the origin alone (CMP-03b).

// FR of docs/11 §4: beyond Capacity the origin refuses at once and counts
// the refusal apart from calls; a pooled profile shares the capacity.
func TestOriginCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := direct()
		ctx := t.Context()
		for name, p := range map[string]profile{"cap": {DelayMS: 100, Capacity: 3}, "pooled": {DelayMS: 100, Pool: "cap"}} {
			if err := r.ctl(ctx, http.MethodPut, "/profile/"+name, p, nil); err != nil {
				t.Fatal(err)
			}
		}
		status := map[string]int{}
		r.fire(ctx, []string{r.target + "/o/cap/a", r.target + "/o/cap/b", r.target + "/o/pooled/c", r.target + "/o/pooled/d", r.target + "/o/cap/e"}, status)
		var st map[string]counters
		if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
			t.Fatal(err)
		}
		calls, refused := st["cap"].Calls+st["pooled"].Calls, st["cap"].Refused+st["pooled"].Refused
		if status["200"] != 3 || status["503"] != 2 || calls != 3 || refused != 2 || st["cap"].Peak > 3 || st["pooled"].Peak > 3 {
			t.Fatalf("status %v, stats %+v", status, st)
		}
		// The slots are free again once the three have been answered.
		if got := r.get(ctx, r.target+"/o/cap/f"); got != "200" {
			t.Fatalf("after the burst: %s, want 200", got)
		}
	})
}

// A profile replaced midway changes the status of the next request, keeps
// the counters, and keeps the requests already in flight against the
// capacity (the first round dropped them).
func TestOriginStatusChangeKeepsCountersAndInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := direct()
		ctx := t.Context()
		put := func(p profile) {
			t.Helper()
			if err := r.ctl(ctx, http.MethodPut, "/profile/x", p, nil); err != nil {
				t.Fatal(err)
			}
		}
		put(profile{DelayMS: 1000, Capacity: 2})
		var wg sync.WaitGroup
		slow := make([]string, 2)
		for i := range slow {
			wg.Go(func() { slow[i] = r.get(ctx, r.target+"/o/x/slow") })
		}
		synctest.Wait() // both are in the origin, waiting out the delay
		put(profile{Status: 503, Capacity: 2})
		if got := r.get(ctx, r.target+"/o/x/now"); got != "503" {
			t.Fatalf("request after the change: %s, want the refusal of a full origin", got)
		}
		var st map[string]counters
		if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
			t.Fatal(err)
		}
		if st["x"].Calls != 2 || st["x"].Refused != 1 {
			t.Fatalf("stats %+v, want the 2 calls kept and 1 refusal", st["x"])
		}
		wg.Wait()
		if slow[0] != "200" || slow[1] != "200" {
			t.Fatalf("requests in flight at the change: %v, want the old profile's 200", slow)
		}
		if got := r.get(ctx, r.target+"/o/x/later"); got != "503" {
			t.Fatalf("request with the origin free: %s, want the new profile's 503", got)
		}
		if err := r.ctl(ctx, http.MethodGet, "/stats", nil, &st); err != nil {
			t.Fatal(err)
		}
		if st["x"].Calls != 3 || st["x"].Refused != 1 {
			t.Fatalf("stats %+v, want 3 calls and 1 refusal", st["x"])
		}
	})
}

// A hanging origin answers after its delay and not before, and a client
// that gives up frees its place.
func TestOriginHang(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := direct()
		ctx := t.Context()
		if err := r.ctl(ctx, http.MethodPut, "/profile/hang", profile{DelayMS: 15000, Capacity: 1}, nil); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if got := r.get(ctx, r.target+"/o/hang/a"); got != "200" || time.Since(start) != 15*time.Second {
			t.Fatalf("%s after %s, want 200 after exactly 15s", got, time.Since(start))
		}
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if got := r.get(short, r.target+"/o/hang/b"); got != "error" {
			t.Fatalf("client that waited 1s got %s, want error", got)
		}
		synctest.Wait()
		if got := r.get(ctx, r.target+"/o/hang/c"); got != "200" {
			t.Fatalf("after the client left: %s, want 200 (its place was freed)", got)
		}
	})
}

func TestOriginControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := direct()
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
	})
}

func TestReportMedians(t *testing.T) {
	write := func(rows ...row) string {
		path := filepath.Join(t.TempDir(), "r.jsonl")
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, r := range rows {
			_ = enc.Encode(r)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var rows []row
	for i, calls := range []int64{9, 1, 2} {
		rows = append(rows, row{Subject: "s", Tier: "default", Scenario: "cold-herd", Rep: i, Requests: 10,
			Status: map[string]int{"200": 8, "error": 1, "bad-body:200": 1}, OriginCalls: calls, OriginPeak: 3})
	}
	rows = append(rows, row{Subject: "s", Tier: "default", Scenario: "purge-herd", NotAvailable: "no purge in this configuration"})
	var out strings.Builder
	if err := reportCmd([]string{write(rows...)}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| cold-herd | s | default | 10 | 2 | 2 | 3 | 0 | 0 |", // an empty 200 is not a 2xx
		"| purge-herd | s | default | not available |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report:\n%s\nwant a line %q", out.String(), want)
		}
	}
	// A control row that saw fewer requests than were sent fails the report.
	bad := write(row{Subject: "origin", Tier: "default", Scenario: "cold-herd", Requests: 10, Status: map[string]int{"200": 10}, Sent: 10, Seen: 9})
	if err := reportCmd([]string{bad}, io.Discard); err == nil {
		t.Fatal("a violated control was reported without an error")
	}
}
