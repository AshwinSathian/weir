//go:build integration

package valkey

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/AshwinSathian/weir/store"
	"github.com/AshwinSathian/weir/store/storetest"
)

// serverAddr returns the test server address. CI sets WEIR_VALKEY_ADDR; a
// developer without a server gets a skip, but CI (CI=true) must not skip.
func serverAddr(t *testing.T) string {
	t.Helper()
	a := os.Getenv("WEIR_VALKEY_ADDR")
	if a == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("WEIR_VALKEY_ADDR is not set in CI")
		}
		t.Skip("WEIR_VALKEY_ADDR is not set; start a Valkey server and export it (make test-valkey)")
	}
	return a
}

// maxHardForConformance is the hard-epoch cap the conformance stores use, so
// EpochHardCap fills it quickly.
const maxHardForConformance = 20

// FR-STF-2, S-1..S-4, 05 §8: the conformance suite against a real server.
// Every epoch mode runs, EpochNeverUnderInvalidates from 64 goroutines (valkey-go
// pipelines concurrent callers). ExpiredIsNotFound runs here on the real clock
// because the server expires keys on its own clock (CLAUDE.md hard rule 6).
func TestStoreConformance(t *testing.T) {
	addr := serverAddr(t)
	// A fresh prefix per store and per run: keys from an earlier run live on
	// the server for up to an hour.
	run := strconv.FormatInt(time.Now().UnixNano(), 36)
	n := 0
	storetest.Run(t, func(t *testing.T) store.Store {
		n++
		s, err := New(Config{Addrs: []string{addr}, Prefix: "t" + run + "x" + strconv.Itoa(n), MaxHardEpochs: maxHardForConformance})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}, storetest.HardEpochCap(maxHardForConformance), storetest.Parallel(64))
}

// E-11: the clamped expiry reaches the server as a TTL no longer than
// MaxRetention (the fake client cannot show what PXAT does).
func TestSetTTLIsClamped(t *testing.T) {
	addr := serverAddr(t)
	s, err := New(Config{Addrs: []string{addr}, Prefix: "ttl" + strconv.FormatInt(time.Now().UnixNano(), 36), MaxRetention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var k store.Key
	now := time.Now()
	e := testEntry(now)
	e.Expires = now.Add(48 * time.Hour)
	if err := s.Set(t.Context(), k, e); err != nil {
		t.Fatal(err)
	}
	cl, err := s.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := cl.(valkeyClient).c.Do(t.Context(), cl.(valkeyClient).c.B().Pttl().Key(s.entryKey(k)).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > time.Hour.Milliseconds() {
		t.Fatalf("PTTL = %d ms, want in (0, %d]", ttl, time.Hour.Milliseconds())
	}
}
