package missrate

import (
	"strings"
	"sync"
	"time"
)

// maxSample is the longest partition string a counter keeps (FR-MR-2).
const maxSample = 256

// Config holds the tracker's settings. The engine fills defaults (04 §5).
type Config struct {
	Window    time.Duration // length of one counting window
	TopK      int           // counters in the summary
	MinMisses int           // misses in a window before a partition may be reported
	MinRatio  float64       // misses / requests that reports it, (0, 1]
}

// Anomaly is one partition that crossed both thresholds in a closed window.
// Reqs and Misses count only the span the partition held its counter, so
// they are lower bounds for the window.
type Anomaly struct {
	Partition    uint64 // partition hash
	Sample       string // partition string, at most 256 bytes
	Reqs, Misses uint64
}

// Tracker is a Space-Saving summary of requests and misses per partition
// (ADR-9). A nil Tracker is valid and records nothing.
type Tracker struct {
	mu       sync.Mutex
	cfg      Config
	start    time.Time
	counters []counter      // len <= TopK (P5)
	index    map[uint64]int // partition hash -> counters index, len <= TopK
	emit     func([]Anomaly)
}

type counter struct {
	h            uint64
	sample       string // set when the counter is (re)assigned
	reqs, misses uint64
	err          uint64 // overestimation bound inherited on replacement
}

// New returns a tracker whose first window starts now. emit is called once
// per closed window with that window's anomalies, an empty slice included,
// outside the tracker's lock; a receiver that keeps only the last call
// (the limiter's throttle map, FR-MR-3) therefore forgets a partition one
// window after it stops being anomalous. Two Observe calls a full window
// apart can deliver their windows out of order.
// New returns nil, a tracker that records nothing, when cfg cannot hold a
// summary (no counters or no window).
func New(cfg Config, emit func([]Anomaly)) *Tracker {
	if cfg.TopK <= 0 || cfg.Window <= 0 {
		return nil
	}
	return &Tracker{
		cfg:      cfg,
		start:    time.Now(),
		counters: make([]counter, 0, cfg.TopK),
		index:    make(map[uint64]int, cfg.TopK),
		emit:     emit,
	}
}

// Observe records one request for partition h; sample is its partition
// string. If the window has ended, Observe first closes it, reports its
// anomalies and counts this request in the new window. There is no timer:
// an idle tracker reports its last window on the next request.
func (t *Tracker) Observe(h uint64, sample string, miss bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	var found []Anomaly
	now := time.Now()
	closed := now.Sub(t.start) >= t.cfg.Window
	if closed {
		found = t.rotate(now)
	}
	t.count(h, sample, miss)
	t.mu.Unlock()
	// P8: emit may log, or call back into Observe, so it runs unlocked.
	if closed && t.emit != nil {
		t.emit(found)
	}
}

func (t *Tracker) count(h uint64, sample string, miss bool) {
	i, ok := t.index[h]
	switch {
	case ok:
		t.counters[i].reqs++
	case len(t.counters) < t.cfg.TopK:
		i = len(t.counters)
		t.counters = append(t.counters, counter{h: h, sample: truncate(sample), reqs: 1})
		t.index[h] = i
	default:
		// ponytail: O(TopK) scan for the minimum under the mutex, about 64
		// compares at the default. Past a few hundred counters, keep them in
		// the stream-summary bucket list of the Space-Saving paper.
		i = 0
		for j := range t.counters {
			if t.counters[j].reqs < t.counters[i].reqs {
				i = j
			}
		}
		lowest := t.counters[i].reqs
		delete(t.index, t.counters[i].h)
		t.counters[i] = counter{h: h, sample: truncate(sample), reqs: lowest + 1, err: lowest}
		t.index[h] = i
	}
	if miss {
		t.counters[i].misses++
	}
}

// rotate closes the window: it returns the partitions over both thresholds
// and empties the summary. The caller holds t.mu.
func (t *Tracker) rotate(now time.Time) []Anomaly {
	var found []Anomaly
	for i := range t.counters {
		c := &t.counters[i]
		// reqs - err is what the counter saw itself, so a count inherited
		// from the partition it replaced cannot hide a flood or fake one.
		// misses <= reqs - err, so the ratio is at most 1. The floor of one
		// miss keeps zero thresholds from naming every tracked partition.
		if c.misses >= uint64(max(t.cfg.MinMisses, 1)) &&
			float64(c.misses) >= t.cfg.MinRatio*float64(c.reqs-c.err) {
			found = append(found, Anomaly{Partition: c.h, Sample: c.sample, Reqs: c.reqs - c.err, Misses: c.misses})
		}
	}
	t.counters = t.counters[:0]
	clear(t.index)
	t.start = now
	return found
}

// truncate copies the kept bytes, so a counter never pins a longer
// partition string for the window (P5).
func truncate(s string) string {
	if len(s) > maxSample {
		return strings.Clone(s[:maxSample])
	}
	return s
}
