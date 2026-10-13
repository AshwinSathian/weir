package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// profile is how the origin answers every path under /o/<name>/. A scenario
// installs one through the control API before it sends load.
type profile struct {
	DelayMS      int    `json:"delay_ms"`
	Status       int    `json:"status"` // 0 means 200
	CacheControl string `json:"cache_control"`
	BodyBytes    int    `json:"body_bytes"`
	// Capacity, above zero, is how many requests the origin serves at once
	// under this profile. One more is refused at once with 503, as an
	// application server at its worker limit does. Refused requests are
	// counted apart from calls.
	Capacity int `json:"capacity"`
	// Pool, when set, names another profile whose Capacity and in-flight
	// count this one shares: two kinds of traffic then compete for one
	// origin's workers while their calls are still counted apart.
	Pool string `json:"pool,omitempty"`
}

// counters is what a scenario reads back: how many requests reached the
// origin for a profile, and the most that were in flight at once.
type counters struct {
	Calls   int64 `json:"calls"`
	Peak    int   `json:"peak"`
	Refused int64 `json:"refused"` // turned away by Capacity
}

const (
	maxProfiles  = 64        // bound on the profile table
	maxBodyBytes = 128 << 20 // bound on one profile's body
)

type slot struct {
	p        profile
	body     []byte
	c        counters
	inflight int
}

type origin struct {
	// ponytail: one lock for every counter. It is far from the bottleneck
	// while scenarios count origin calls; the speed pillar (CMP-06) should
	// move to per-profile atomics if the origin shows up in a profile.
	mu    sync.Mutex
	slots map[string]*slot // at most maxProfiles
	seq   atomic.Int64
}

func newOrigin() *origin {
	// "ready" is what the runner polls through a cache to learn it is up.
	return &origin{slots: map[string]*slot{
		"ready": {p: profile{Status: http.StatusOK, CacheControl: "no-store"}, body: []byte("ok")},
	}}
}

func (o *origin) data() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/o/{profile}/{key...}", o.serve)
	mux.HandleFunc("/p/{probe}/{key...}", probeOrigin) // the unsafe routes of the security probes
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The path probe needs an origin that does not normalize, and the
		// mux redirects any path it would clean. That route is served
		// before the mux sees it.
		if strings.HasPrefix(r.URL.Path, "/p/pathnorm/") {
			probeOrigin(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (o *origin) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	s := o.slots[r.PathValue("profile")]
	if s == nil {
		o.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	pool := s
	if ps := o.slots[s.p.Pool]; ps != nil {
		pool = ps
	}
	if pool.p.Capacity > 0 && pool.inflight >= pool.p.Capacity {
		s.c.Refused++
		o.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "origin at capacity", http.StatusServiceUnavailable)
		return
	}
	s.c.Calls++
	call := s.c.Calls
	pool.inflight++
	s.c.Peak = max(s.c.Peak, pool.inflight)
	p, body := s.p, s.body
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		pool.inflight--
		o.mu.Unlock()
	}()

	if p.DelayMS > 0 {
		t := time.NewTimer(time.Duration(p.DelayMS) * time.Millisecond)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			return
		}
	}
	h := w.Header()
	if p.CacheControl != "" {
		h.Set("Cache-Control", p.CacheControl)
	}
	h.Set("Content-Type", "text/plain")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	// Which origin call produced this response: lets a probe tell a cached
	// copy from a fresh one.
	h.Set("X-Origin-Call", strconv.FormatInt(call, 10))
	// The same, but never reset: a scenario that zeroes the counters midway
	// can still tell a response made before that point from one made after.
	h.Set("X-Origin-Seq", strconv.FormatInt(o.seq.Add(1), 10))
	w.WriteHeader(p.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (o *origin) control() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /profile/{name}", o.putProfile)
	mux.HandleFunc("GET /stats", o.stats)
	mux.HandleFunc("POST /reset", o.reset)
	return mux
}

func (o *origin) putProfile(w http.ResponseWriter, r *http.Request) {
	var p profile
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if p.Status == 0 {
		p.Status = http.StatusOK
	}
	if p.Status < 200 || p.Status > 599 || p.DelayMS < 0 || p.BodyBytes < 0 || p.BodyBytes > maxBodyBytes || p.Capacity < 0 {
		http.Error(w, "profile out of range", http.StatusBadRequest)
		return
	}
	name := r.PathValue("name")
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.slots[name]; !ok && len(o.slots) >= maxProfiles {
		http.Error(w, "too many profiles", http.StatusInsufficientStorage)
		return
	}
	// A profile that is replaced keeps its counters and its in-flight
	// count: a scenario may change the origin's behavior midway and still
	// read one total, and requests that began under the old profile still
	// occupy the capacity until they finish. So the slot is changed in
	// place, never swapped.
	s := o.slots[name]
	if s == nil {
		s = &slot{}
		o.slots[name] = s
	}
	s.p, s.body = p, bytes.Repeat([]byte{'x'}, p.BodyBytes)
	w.WriteHeader(http.StatusNoContent)
}

func (o *origin) stats(w http.ResponseWriter, _ *http.Request) {
	o.mu.Lock()
	out := make(map[string]counters, len(o.slots))
	for name, s := range o.slots {
		out[name] = s.c
	}
	o.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (o *origin) reset(w http.ResponseWriter, _ *http.Request) {
	o.mu.Lock()
	for _, s := range o.slots {
		s.c = counters{}
	}
	o.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
