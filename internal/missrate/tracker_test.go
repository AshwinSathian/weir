package missrate

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func testConfig() Config {
	return Config{Window: 10 * time.Second, TopK: 64, MinMisses: 500, MinRatio: 0.9}
}

// collect returns a tracker and the anomalies it has emitted so far.
func collect(cfg Config) (*Tracker, *[]Anomaly) {
	var got []Anomaly
	return New(cfg, func(a []Anomaly) { got = append(got, a...) }), &got
}

// rotate ends the current window and makes the observation that notices it.
func rotate(tr *Tracker, cfg Config) {
	time.Sleep(cfg.Window)
	tr.Observe(1<<63, "/rotate", false)
}

// FR-MR-1, FR-MR-2, T-11: 100 000 distinct partitions cannot push the heavy
// hitter out of the summary or grow it past TopK counters.
func TestSpaceSavingBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		tr, got := collect(cfg)
		const heavy = uint64(7)
		for i := range 125_000 {
			if i%5 == 0 {
				tr.Observe(heavy, "/heavy", true)
			} else {
				tr.Observe(1000+uint64(i), "/one-off", true)
			}
			if len(tr.counters) > cfg.TopK || len(tr.index) > cfg.TopK {
				t.Fatalf("after %d observations: %d counters, %d index entries, want at most %d",
					i+1, len(tr.counters), len(tr.index), cfg.TopK)
			}
		}
		if cap(tr.counters) != cfg.TopK {
			t.Errorf("cap(counters) = %d, want %d", cap(tr.counters), cfg.TopK)
		}
		rotate(tr, cfg)
		// The 63 other counters each hold a few misses since their last
		// reassignment, far under MinMisses, so only the heavy hitter is named.
		if len(*got) != 1 {
			t.Fatalf("anomalies = %+v, want only the heavy hitter", *got)
		}
		want := Anomaly{Partition: heavy, Sample: "/heavy", Reqs: 25_000, Misses: 25_000}
		if (*got)[0] != want {
			t.Errorf("anomaly = %+v, want %+v", (*got)[0], want)
		}
	})
}

// FR-MR-2: both thresholds must hold, and the ratio is taken over the span
// the counter was tracked for, not over its inherited count.
func TestAnomalyThresholds(t *testing.T) {
	tests := []struct {
		name         string
		misses, hits int
		want         bool
	}{
		{"all misses at MinMisses is reported", 500, 0, true},
		{"one miss under MinMisses is not", 499, 0, false},
		{"ratio exactly MinRatio is reported", 900, 100, true},
		{"ratio just under MinRatio is not", 899, 101, false},
		{"mostly hits is not", 500, 5000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := testConfig()
				tr, got := collect(cfg)
				for range tc.misses {
					tr.Observe(1, "/p", true)
				}
				for range tc.hits {
					tr.Observe(1, "/p", false)
				}
				rotate(tr, cfg)
				if (len(*got) == 1) != tc.want {
					t.Errorf("anomalies = %+v, want reported = %v", *got, tc.want)
				}
			})
		})
	}

	t.Run("a replaced counter is judged on its own span", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			cfg := testConfig()
			cfg.TopK = 2
			tr, got := collect(cfg)
			// Two partitions served from cache fill the summary with 10 000
			// requests each; the flood then takes over one counter and
			// inherits its count as err.
			for range 10_000 {
				tr.Observe(1, "/a", false)
				tr.Observe(2, "/b", false)
			}
			for range 600 {
				tr.Observe(3, "/flood", true)
			}
			rotate(tr, cfg)
			want := Anomaly{Partition: 3, Sample: "/flood", Reqs: 600, Misses: 600}
			if len(*got) != 1 || (*got)[0] != want {
				t.Errorf("anomalies = %+v, want only %+v", *got, want)
			}
		})
	})
}

// FR-MR-1: the window rotates on the next observation, with no timer or
// goroutine, and a new window starts empty.
func TestLazyWindowRotation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		tr, got := collect(cfg)
		for range 600 {
			tr.Observe(1, "/p", true)
		}
		time.Sleep(cfg.Window - time.Nanosecond)
		tr.Observe(1, "/p", true)
		if len(*got) != 0 {
			t.Fatalf("emitted %+v before the window ended", *got)
		}
		time.Sleep(3 * cfg.Window) // idle: nothing runs, nothing is emitted
		synctest.Wait()
		if len(*got) != 0 {
			t.Fatalf("emitted %+v with no observation", *got)
		}
		tr.Observe(2, "/q", true)
		if len(*got) != 1 || (*got)[0].Misses != 601 {
			t.Fatalf("anomalies = %+v, want one with 601 misses", *got)
		}
		// The rotating observation belongs to the new window.
		if len(tr.counters) != 1 || tr.counters[0].h != 2 || tr.counters[0].reqs != 1 {
			t.Errorf("counters after rotation = %+v, want only partition 2 with 1 request", tr.counters)
		}
		// So does its index: partition 1 gets a counter of its own again.
		tr.Observe(1, "/p", true)
		if len(tr.counters) != 2 || tr.counters[0].reqs != 1 || tr.counters[1].h != 1 {
			t.Errorf("counters = %+v, want partition 1 added beside partition 2", tr.counters)
		}
		rotate(tr, cfg)
		if len(*got) != 1 {
			t.Errorf("anomalies = %+v, the old window was reported twice", *got)
		}
	})
}

// FR-MR-2: the sample is cut to 256 bytes.
func TestSampleTruncated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		tr, got := collect(cfg)
		long := strings.Repeat("a", 300)
		for range 500 {
			tr.Observe(1, long, true)
		}
		rotate(tr, cfg)
		if len(*got) != 1 || (*got)[0].Sample != long[:256] {
			t.Errorf("anomalies = %+v, want one sample of 256 bytes", *got)
		}
	})
}

// FR-MR-1, NFR-2: a nil tracker, and one New refuses to build, accept calls.
func TestNilTracker(t *testing.T) {
	var tr *Tracker
	tr.Observe(1, "/p", true)
	for _, cfg := range []Config{
		{Window: time.Second, TopK: 0},
		{Window: 0, TopK: 64},
		{Window: time.Second, TopK: -1},
	} {
		if tr := New(cfg, func([]Anomaly) {}); tr != nil {
			t.Errorf("New(%+v) = %v, want nil", cfg, tr)
		} else {
			tr.Observe(1, "/p", true)
		}
	}
}

// P8, NFR-2: Observe is safe from many goroutines and emit runs outside the
// lock, so a callback that observes does not deadlock.
func TestObserveConcurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		var tr *Tracker
		var mu sync.Mutex
		var misses uint64
		tr = New(cfg, func(as []Anomaly) {
			tr.Observe(99, "/from-emit", false)
			mu.Lock()
			for _, a := range as {
				misses += a.Misses
			}
			mu.Unlock()
		})
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 1000 {
					tr.Observe(1, "/p", true)
				}
			})
		}
		wg.Wait()
		rotate(tr, cfg)
		if misses != 8000 {
			t.Errorf("misses = %d, want 8000", misses)
		}
	})
}

// FR-MR-3: every closed window is reported once, an empty one included, so a
// receiver that keeps the last report drops a throttle after one quiet window.
func TestEmitOncePerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		var calls [][]Anomaly
		tr := New(cfg, func(a []Anomaly) { calls = append(calls, a) })
		for range 600 {
			tr.Observe(1, "/p", true)
		}
		rotate(tr, cfg) // closes the flood window
		rotate(tr, cfg) // closes a quiet one
		if len(calls) != 2 || len(calls[0]) != 1 || len(calls[1]) != 0 {
			t.Errorf("calls = %+v, want one anomaly, then an empty report", calls)
		}
	})
}

// NFR-2: zero or negative thresholds do not name partitions with no misses,
// and a nil emit is accepted.
func TestDegenerateConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.MinMisses, cfg.MinRatio = -1, 0
		tr, got := collect(cfg)
		tr.Observe(1, "/hits", false)
		tr.Observe(2, "/miss", true)
		rotate(tr, cfg)
		if len(*got) != 1 || (*got)[0].Partition != 2 {
			t.Errorf("anomalies = %+v, want only the partition with a miss", *got)
		}
		quiet := New(cfg, nil)
		quiet.Observe(1, "/p", true)
		rotate(quiet, cfg)
	})
}
