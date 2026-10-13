package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"testing"
)

// naiveCache stores the first response per cleaned path and serves it to
// every later request: a cache that ignores the query, the request headers,
// the method, the status and Vary, and that normalizes the path for its key
// while it forwards the request as it came. The probes must call it unsafe.
func naiveCache(origin string) http.Handler {
	type entry struct {
		status int
		h      http.Header
		body   []byte
	}
	var mu sync.Mutex
	m := map[string]entry{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := path.Clean(r.URL.Path)
		mu.Lock()
		e, ok := m[key]
		mu.Unlock()
		if !ok {
			req, _ := http.NewRequestWithContext(r.Context(), r.Method, origin+r.RequestURI, r.Body)
			req.Header = r.Header.Clone()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			e = entry{resp.StatusCode, resp.Header, b}
			mu.Lock()
			m[key] = e
			mu.Unlock()
		}
		for k, v := range e.h {
			w.Header()[k] = v
		}
		w.WriteHeader(e.status)
		_, _ = w.Write(e.body)
	})
}

// docs/11 §5.3: against the origin alone nothing is reused, so no probe is
// unsafe; through a cache that keys on the path and stores everything, every
// probe is. A probe that cannot tell the two apart measures nothing.
func TestProbesSeparateOriginFromNaiveCache(t *testing.T) {
	defer func(n int) { varyN = n }(varyN)
	varyN = 300
	o := newOrigin()
	origin := httptest.NewServer(o.data())
	defer origin.Close()
	naive := httptest.NewServer(naiveCache(origin.URL))
	defer naive.Close()

	seen := map[string]bool{}
	for _, row := range runProbes(t.Context(), http.DefaultClient, origin.URL, "origin", "default") {
		seen[row.Probe] = true
		switch row.Probe {
		case "no-cache-amplify":
			if row.Verdict != "50 of 50" {
				t.Errorf("origin direct: %s = %q, want every request at the origin", row.Probe, row.Verdict)
			}
		default:
			if row.Verdict != "safe" {
				t.Errorf("origin direct: %s = %s (%s), want safe", row.Probe, row.Verdict, row.Why)
			}
		}
		if row.Probe == "vary-cardinality" && row.Figure != "0 of 200" {
			t.Errorf("origin direct: %s kept %s, want nothing kept", row.Probe, row.Figure)
		}
	}
	for _, name := range []string{"cloak", "pathnorm", "meta", "vary-cardinality", "error-as-success"} {
		if !seen[name] {
			t.Errorf("probe %s did not run", name)
		}
	}
	for _, row := range runProbes(t.Context(), http.DefaultClient, naive.URL, "naive", "default") {
		switch row.Probe {
		case "no-cache-amplify":
			if row.Verdict != "0 of 50" {
				t.Errorf("naive cache: %s = %q, want none at the origin", row.Probe, row.Verdict)
			}
		case "error-as-success":
			// A cache that replays a stored 500 is not serving a success.
			if row.Verdict != "safe" {
				t.Errorf("naive cache: %s = %s (%s), want safe: a replayed 500 is still an error", row.Probe, row.Verdict, row.Why)
			}
		default:
			if row.Verdict != "unsafe" {
				t.Errorf("naive cache: %s = %s (%s), want unsafe", row.Probe, row.Verdict, row.Why)
			}
		}
	}
}

// The concurrent error probe exists for one behavior: a cache that answers
// the waiters of a failed fetch with an empty 200 (darkweak/souin#876). A
// stand-in that does exactly that must be called unsafe.
func TestErrorProbeCatchesEmptySuccessForWaiters(t *testing.T) {
	defer func(n int) { varyN = n }(varyN)
	varyN = 2
	o := newOrigin()
	origin := httptest.NewServer(o.data())
	defer origin.Close()
	var mu sync.Mutex
	inflight := map[string]chan struct{}{}
	coalescer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		wait, follower := inflight[r.URL.Path]
		if !follower {
			wait = make(chan struct{})
			inflight[r.URL.Path] = wait
		}
		mu.Unlock()
		if follower {
			<-wait // then 200 with nothing in it
			return
		}
		resp, err := http.Get(origin.URL + r.RequestURI) //nolint:noctx,gosec // a test stand-in
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(b)
		}
		mu.Lock()
		delete(inflight, r.URL.Path)
		mu.Unlock()
		close(wait)
	}))
	defer coalescer.Close()
	for _, row := range runProbes(t.Context(), http.DefaultClient, coalescer.URL, "coalescer", "default") {
		if row.Probe == "error-as-success" && (row.Verdict != "unsafe" || row.Victim.Status != http.StatusOK || row.Victim.Body != "") {
			t.Fatalf("%s = %s (%s), victim %+v; want unsafe with an empty 200", row.Probe, row.Verdict, row.Why, row.Victim)
		}
	}
}
